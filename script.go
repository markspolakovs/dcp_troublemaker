package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/couchbase/gocbcore/v10/memd"
	"github.com/robertkrimen/otto"
	"github.com/rs/zerolog"
)

type PacketScripts struct {
	fileName string
	vm       *otto.Otto
	execLock sync.Mutex
}

func LoadScriptsFromFile(fileName string, contents []byte) (*PacketScripts, error) {
	ps := &PacketScripts{
		fileName: filepath.Base(fileName),
		vm:       otto.New(),
	}
	var err error
	script, err := ps.vm.Compile(fileName, contents)
	if err != nil {
		return nil, fmt.Errorf("compilation error: %w", err)
	}
	_, err = ps.vm.Run(script)
	if err != nil {
		return nil, fmt.Errorf("evaluation error: %w", err)
	}
	return ps, nil
}

var ErrFuncNotDefined = errors.New("function not defined")

func (ps *PacketScripts) GetFuncForOp(cmd memd.CmdCode) (otto.Value, error) {
	fnName := cmd.Name()
	val, err := ps.vm.Get(fnName)
	if err != nil {
		return otto.Value{}, err
	}
	if val.IsUndefined() {
		return otto.Value{}, ErrFuncNotDefined
	}
	if !val.IsFunction() {
		return otto.Value{}, fmt.Errorf("field %q is not a function", fnName)
	}
	return val, nil
}

func packetFromObject(obj *otto.Object) *memd.Packet {
	magic := objCast(obj, "Magic", parseMagic, false)
	return &memd.Packet{
		Magic:        magic,
		Command:      objCast(obj, "Command", parseCmd, false),
		Datatype:     objCast(obj, "Datatype", parseU8, false),
		Status:       objCast(obj, "Status", parseStatus, false),
		Vbucket:      objCast(obj, "Vbucket", parseU16, magic == memd.CmdMagicRes),
		Opaque:       objCast(obj, "Opaque", parseU32, true),
		Cas:          objCast(obj, "Cas", parseU64, true),
		CollectionID: objCast(obj, "CollectionID", parseU32, true),
		Key:          objCast(obj, "Key", parseByteArray, true),
		Extras:       objCast(obj, "Extras", parseByteArray, true),
		Value:        objCast(obj, "Value", parseByteArray, true),
		//BarrierFrame:           nil,
		//DurabilityLevelFrame:   nil,
		//DurabilityTimeoutFrame: nil,
		//StreamIDFrame:          nil,
		//OpenTracingFrame:       nil,
		//ServerDurationFrame:    nil,
		//UserImpersonationFrame: nil,
		//PreserveExpiryFrame:    nil,
		//UnsupportedFrames:      nil,
	}
}

func (ps *PacketScripts) EvaluateScriptForPacket(ctx context.Context, logger zerolog.Logger, packet *memd.Packet, cameFromBE bool, be, fe chan<- packetWrapper) error {
	forward := func(pkt packetWrapper) {
		if cameFromBE {
			select {
			case fe <- pkt:
			case <-ctx.Done():
				logger.Warn().Err(ctx.Err()).Msg("dropping packet")
			}
		} else {
			select {
			case be <- pkt:
			case <-ctx.Done():
				logger.Warn().Err(ctx.Err()).Msg("dropping packet")
			}
		}
	}
	reply := func(pkt packetWrapper) {
		if cameFromBE {
			select {
			case be <- pkt:
			case <-ctx.Done():
				logger.Warn().Err(ctx.Err()).Msg("dropping packet")
			}
		} else {
			select {
			case fe <- pkt:
			case <-ctx.Done():
				logger.Warn().Err(ctx.Err()).Msg("dropping packet")
			}
		}
	}

	fn, err := ps.GetFuncForOp(packet.Command)
	if err != nil {
		if errors.Is(err, ErrFuncNotDefined) {
			// fallback to forward
			forward(packetWrapper{packet: packet})
			return nil
		}
		return err
	}
	ps.execLock.Lock()
	defer ps.execLock.Unlock()
	ps.DefineScriptGlobals(logger, forward, reply)
	packetVal, err := ps.vm.ToValue(packet)
	if err != nil {
		panic(err)
	}
	_, err = fn.Call(otto.UndefinedValue(), packetVal)
	return err
}

func (ps *PacketScripts) Copy() *PacketScripts {
	ps2 := &PacketScripts{
		vm:       ps.vm.Copy(),
		fileName: ps.fileName,
	}
	return ps2
}
