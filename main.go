package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/couchbase/gocbcore/v10/memd"
	"go.uber.org/atomic"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	BackendHost = kingpin.Flag("backend-host", "backend host").Default("127.0.0.1").String()
	BackendPort = kingpin.Flag("backend-port", "backend port").Default("11210").Int()
	ListenPort  = kingpin.Flag("listen-port", "port to listen on").Default("11210").Int()
	Verbosity   = kingpin.Flag("verbose", "verbosity").Short('v').Counter()
	ScriptPath  = kingpin.Arg("script-path", "path to js").String()
)

// logPacket logs the details of the packet, respecting Verbosity.
func logPacket(isBEToFE bool, cid uint64, packet *memd.Packet) {
	prefix := "-->"
	if isBEToFE {
		prefix = "<--"
	}
	switch *Verbosity {
	case 0:
	case 1:
		// log just the opaque, magic, opcode, and result
		if isBEToFE {
			log.Printf("[%d] %s opaque 0x%x magic %s opcode 0x%x (%s) result %s", cid, prefix, packet.Opaque, packet.Magic.String(), packet.Command, packet.Command.Name(), packet.Status.String())
		} else {
			log.Printf("[%d] %s opaque 0x%x magic %s opcode 0x%x (%s)", cid, prefix, packet.Opaque, packet.Magic.String(), packet.Command, packet.Command.Name())
		}
	case 2:
		// also include the key and value
		var key string
		if utf8.Valid(packet.Key) {
			key = string(packet.Key)
		} else {
			key = hex.EncodeToString(packet.Key)
		}
		var value string
		if utf8.Valid(packet.Value) {
			value = string(packet.Value)
		} else {
			value = hex.EncodeToString(packet.Value)
		}
		if isBEToFE {
			log.Printf("[%d] %s opaque 0x%x magic %s opcode 0x%x (%s) result %s\nkey: %v\nvalue: %v", cid, prefix, packet.Opaque, packet.Magic.String(), packet.Command, packet.Command.Name(), packet.Status.String(), key, value)
		} else {
			log.Printf("[%d] %s opaque 0x%x magic %s opcode 0x%x (%s)\nkey: %v\nvalue: %v", cid, prefix, packet.Opaque, packet.Magic.String(), packet.Command, packet.Command.Name(), key, value)
		}
	case 3:
		// full hex-dump of the packet - use memd.Packet's Stringer
		log.Printf("[%d] %s %s", cid, prefix, packet.String())
	}
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

	scriptFile, err := ioutil.ReadFile(*ScriptPath)
	if err != nil {
		panic(err)
	}
	packetScripts, err := LoadScriptsFromFile(filepath.Base(*ScriptPath), scriptFile)
	if err != nil {
		panic(err)
	}

	if *BackendPort == *ListenPort && hostIsLoopback(*BackendHost) {
		log.Println("WARNING: backend_port and listen_port are the same and backend_host is loopback - possible infinite loops!")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	addr := net.JoinHostPort("", strconv.Itoa(*ListenPort))
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	log.Printf("Listening on %s", addr)
	go func() {
		<-ctx.Done()
		log.Println("shutting down!")
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

func handleConn(ctx context.Context, rawConn net.Conn, scripts *PacketScripts) {
	defer rawConn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cid := nextCID.Inc()
	log.Printf("[%d] ohai %s\n", cid, rawConn.RemoteAddr())
	beRawConn, err := net.Dial("tcp", net.JoinHostPort(*BackendHost, strconv.Itoa(*BackendPort)))
	if err != nil {
		panic(err)
	}
	defer beRawConn.Close()
	feConn := memd.NewConn(rawConn)
	beConn := memd.NewConn(beRawConn)

	bePackets := make(chan *memd.Packet)
	fePackets := make(chan *memd.Packet)
	go func() {
		for {
			packet, _, err := feConn.ReadPacket()
			switch {
			case errors.Is(err, io.EOF):
				log.Printf("[%d] fe EOF, goodbye", cid)
				cancel()
				return
			case errors.Is(err, net.ErrClosed), err != nil && strings.HasSuffix(err.Error(), "connection reset by peer"):
				log.Printf("[%d] network closed, goodbye", cid)
				cancel()
				return
			case err != nil:
				panic(err)
			}
			if err := scripts.EvaluateScriptForPacket(ctx, packet, fePackets, bePackets); err != nil {
				log.Printf("[%v] script evaluation error: %v", cid, err)
			}
		}
	}()
	go func() {
		for {
			packet, _, err := beConn.ReadPacket()
			switch {
			case errors.Is(err, io.EOF):
				log.Printf("[%d] be EOF, goodbye", cid)
				cancel()
				return
			case errors.Is(err, net.ErrClosed), err != nil && strings.HasSuffix(err.Error(), "connection reset by peer"):
				log.Printf("[%d] network closed, goodbye", cid)
				cancel()
				return
			case err != nil:
				panic(err)
			}
			if err := scripts.EvaluateScriptForPacket(ctx, packet, fePackets, bePackets); err != nil {
				log.Printf("[%v] script evaluation error: %v", cid, err)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case fe := <-fePackets:
			logPacket(false, cid, fe)
			if err := beConn.WritePacket(fe); err != nil {
				panic(err)
			}
		case be := <-bePackets:
			logPacket(true, cid, be)
			// HELO needs special handling to ensure we enable the features
			if be.Command == memd.CmdHello {
				for feat := 0; feat < (len(be.Value) / 2); feat++ {
					feature := memd.HelloFeature(binary.BigEndian.Uint16(be.Value[feat*2 : (feat+1)*2]))
					// ensure we enable it on both the FE and BE
					feConn.EnableFeature(feature)
					beConn.EnableFeature(feature)
				}
			}
			if err := feConn.WritePacket(be); err != nil {
				panic(err)
			}
		}
	}
}
