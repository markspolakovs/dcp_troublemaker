package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/couchbase/gocbcore/v10/memd"
	"github.com/robertkrimen/otto"
)

type PacketScripts struct {
	vm       *otto.Otto
	execLock sync.Mutex
}

func LoadScriptsFromFile(fileName string, contents []byte) (*PacketScripts, error) {
	ps := new(PacketScripts)
	ps.vm = otto.New()
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

func (ps *PacketScripts) EvaluateScriptForPacket(ctx context.Context, packet *memd.Packet, be, fe chan<- *memd.Packet) error {
	forward := func(pkt *memd.Packet) error {
		switch pkt.Magic {
		case memd.CmdMagicReq:
			select {
			case be <- pkt:
			case <-ctx.Done():
				return ctx.Err()
			}
		case memd.CmdMagicRes:
			select {
			case fe <- pkt:
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			panic(fmt.Errorf("invalid magic %v", pkt.Magic))
		}
		return nil
	}
	reply := func(pkt *memd.Packet) error {
		switch pkt.Magic {
		case memd.CmdMagicReq:
			select {
			case be <- pkt:
			case <-ctx.Done():
				return ctx.Err()
			}
		case memd.CmdMagicRes:
			select {
			case fe <- pkt:
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			panic(fmt.Errorf("invalid magic %v", pkt.Magic))
		}
		return nil
	}

	fn, err := ps.GetFuncForOp(packet.Command)
	if err != nil {
		if errors.Is(err, ErrFuncNotDefined) {
			// fallback to forward
			forward(packet)
			return nil
		}
		return err
	}
	ps.execLock.Lock()
	defer ps.execLock.Unlock()
	ps.vm.Set("forward", func(call otto.FunctionCall) otto.Value {
		newPacketObj := call.Argument(0)
		if !newPacketObj.IsObject() {
			panic("invalid")
		}
		if err := forward(packetFromObject(newPacketObj.Object())); err != nil {
			panic(err)
		}
		return otto.UndefinedValue()
	})
	ps.vm.Set("reply", func(call otto.FunctionCall) otto.Value {
		newPacketObj := call.Argument(0)
		if !newPacketObj.IsObject() {
			panic("invalid")
		}
		if err := reply(packetFromObject(newPacketObj.Object())); err != nil {
			panic(err)
		}
		return otto.UndefinedValue()
	})
	ps.vm.Set("log", func(call otto.FunctionCall) otto.Value {
		log.Printf(call.Argument(0).String(), call.ArgumentList[1:])
		return otto.UndefinedValue()
	})
	packetVal, err := ps.vm.ToValue(packet)
	if err != nil {
		panic(err)
	}
	_, err = fn.Call(otto.UndefinedValue(), packetVal)
	return err
}

func (ps *PacketScripts) Copy() *PacketScripts {
	ps2 := &PacketScripts{
		vm: ps.vm.Copy(),
	}
	return ps2
}
