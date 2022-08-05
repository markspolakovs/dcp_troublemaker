package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/couchbase/gocbcore/v10/memd"
	"go.uber.org/atomic"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	BackendHost = kingpin.Flag("backend-host", "backend host").Default("127.0.0.1").String()
	BackendPort = kingpin.Flag("backend-port", "backend port").Default("11210").Int()
	ListenPort  = kingpin.Flag("listen-port", "port to listen on").Default("11210").Int()
	ScriptPath  = kingpin.Arg("script-path", "path to js").Required().String()
	LogLevel    = kingpin.Flag("log-level", "log level").Default("info").String()
	LogPretty   = kingpin.Flag("log-pretty", "pretty logging").Bool()
)

// logPacket logs the details of the packet, respecting Verbosity.
func logPacket(logger zerolog.Logger, isBEToFE bool, packet *memd.Packet, script string) {
	var entry *zerolog.Event
	if script != "" {
		entry = logger.Info()
	} else if logger.GetLevel() <= zerolog.TraceLevel {
		entry = logger.Trace()
	} else {
		entry = logger.Debug()
	}
	if isBEToFE {
		entry = entry.Str("direction", "be->fe")
	} else {
		entry = entry.Str("direction", "fe->be")
	}
	entry = entry.Uint32("opaque", packet.Opaque).
		Str("magic", packet.Magic.String()).
		Uint8("opcode", uint8(packet.Command)).
		Str("command", packet.Command.Name())
	if script != "" {
		entry = entry.Str("script", script)
	}
	if packet.Magic == memd.CmdMagicRes {
		entry = entry.Uint16("status", uint16(packet.Status)).Str("result", packet.Status.String())
	}
	// at trace, also include the key, value, and extras
	if logger.GetLevel() <= zerolog.TraceLevel {
		if utf8.Valid(packet.Key) {
			entry = entry.Str("key", string(packet.Key))
		} else {
			entry = entry.Bytes("key", packet.Key)
		}
		if utf8.Valid(packet.Value) {
			entry = entry.Str("value", string(packet.Value))
		} else {
			entry = entry.Bytes("value", packet.Value)
		}
		entry = entry.Bytes("extras", packet.Extras)
	}
	entry.Send()
}

func hostIsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
	}
	return false
}

func main() {
	kingpin.Parse()

	logLevel, err := zerolog.ParseLevel(*LogLevel)
	if err != nil {
		panic(err)
	}
	var logOutput io.Writer
	if *LogPretty {
		logOutput = zerolog.NewConsoleWriter()
	} else {
		logOutput = os.Stdout
	}
	log.Logger = zerolog.New(logOutput).With().Timestamp().Logger().Level(logLevel)

	scriptFile, err := ioutil.ReadFile(*ScriptPath)
	if err != nil {
		panic(err)
	}
	packetScripts, err := LoadScriptsFromFile(filepath.Base(*ScriptPath), scriptFile)
	if err != nil {
		panic(err)
	}

	if *BackendPort == *ListenPort && hostIsLoopback(*BackendHost) {
		log.Warn().Msg("WARNING: backend_port and listen_port are the same and backend_host is loopback - possible infinite loops!")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	addr := net.JoinHostPort("", strconv.Itoa(*ListenPort))
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	log.Info().Str("addr", addr).Msg("Listening")
	go func() {
		<-ctx.Done()
		log.Info().Msg("Shutting down!")
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				// likely just closing down
				break
			}
			panic(err)
		}
		go handleConn(ctx, conn, packetScripts.Copy())
	}
}

var nextCID atomic.Uint64

type packetWrapper struct {
	packet *memd.Packet
	script string
}

func handleConn(ctx context.Context, rawConn net.Conn, scripts *PacketScripts) {
	defer rawConn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cid := nextCID.Inc()
	logger := log.With().Uint64("cid", cid).Logger()
	logger.Info().Str("remoteAddr", rawConn.RemoteAddr().String()).Msg("ohai")
	beRawConn, err := net.Dial("tcp", net.JoinHostPort(*BackendHost, strconv.Itoa(*BackendPort)))
	if err != nil {
		panic(err)
	}
	defer beRawConn.Close()
	feConn := memd.NewConn(rawConn)
	beConn := memd.NewConn(beRawConn)

	bePackets := make(chan packetWrapper)
	fePackets := make(chan packetWrapper)
	go func() {
		for {
			packet, _, err := feConn.ReadPacket()
			switch {
			case errors.Is(err, io.EOF):
				logger.Info().Err(err).Str("side", "fe").Msg("goodbye")
				cancel()
				return
			case errors.Is(err, net.ErrClosed), err != nil && strings.HasSuffix(err.Error(), "connection reset by peer"):
				logger.Warn().Err(err).Str("side", "fe").Msg("goodbye")
				cancel()
				return
			case err != nil:
				panic(err)
			}
			logPacket(logger, false, packet, "")
			if err := scripts.EvaluateScriptForPacket(ctx, logger, packet, fePackets, bePackets); err != nil {
				logger.Error().Err(err).Msg("script evaluation error")
			}
		}
	}()
	go func() {
		for {
			packet, _, err := beConn.ReadPacket()
			switch {
			case errors.Is(err, io.EOF):
				logger.Info().Err(err).Str("side", "be").Msg("goodbye")
				cancel()
				return
			case errors.Is(err, net.ErrClosed), err != nil && strings.HasSuffix(err.Error(), "connection reset by peer"):
				logger.Warn().Err(err).Msg("goodbye")
				cancel()
				return
			case err != nil:
				panic(err)
			}
			logPacket(logger, true, packet, "")
			if err := scripts.EvaluateScriptForPacket(ctx, logger, packet, fePackets, bePackets); err != nil {
				logger.Error().Err(err).Str("side", "be").Msg("script evaluation error")
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case fe := <-fePackets:
			if fe.script != "" {
				logPacket(logger, false, fe.packet, fe.script)
			}
			if err := beConn.WritePacket(fe.packet); err != nil {
				panic(err)
			}
		case be := <-bePackets:
			if be.script != "" {
				logPacket(logger, true, be.packet, be.script)
			}
			// HELO needs special handling to ensure we enable the features
			if be.packet.Command == memd.CmdHello {
				for feat := 0; feat < (len(be.packet.Value) / 2); feat++ {
					feature := memd.HelloFeature(binary.BigEndian.Uint16(be.packet.Value[feat*2 : (feat+1)*2]))
					// ensure we enable it on both the FE and BE
					feConn.EnableFeature(feature)
					beConn.EnableFeature(feature)
				}
			}
			if err := feConn.WritePacket(be.packet); err != nil {
				panic(err)
			}
		}
	}
}
