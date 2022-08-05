package main

import (
	"strconv"

	"github.com/robertkrimen/otto"
	"github.com/rs/zerolog"
)

func (ps *PacketScripts) DefineScriptGlobals(logger zerolog.Logger, forward, reply func(packet packetWrapper)) {
	ps.vm.Set("forward", func(call otto.FunctionCall) otto.Value {
		newPacketObj := call.Argument(0)
		if !newPacketObj.IsObject() {
			panic("invalid")
		}
		forward(packetWrapper{
			packet: packetFromObject(newPacketObj.Object()),
			script: ps.fileName,
		})
		return otto.UndefinedValue()
	})
	ps.vm.Set("reply", func(call otto.FunctionCall) otto.Value {
		newPacketObj := call.Argument(0)
		if !newPacketObj.IsObject() {
			panic("invalid")
		}
		reply(packetWrapper{
			packet: packetFromObject(newPacketObj.Object()),
			script: ps.fileName,
		})
		return otto.UndefinedValue()
	})
	ps.vm.Set("stringToBytes", func(call otto.FunctionCall) otto.Value {
		arg := call.Argument(0)
		bytesVal := []byte(arg.String())
		val, _ := ps.vm.ToValue(bytesVal)
		return val
	})
	ps.vm.Set("bytesToString", func(call otto.FunctionCall) otto.Value {
		bytes, err := parseByteArray(call.Argument(0))
		if err != nil {
			panic(err)
		}
		val, _ := ps.vm.ToValue(string(bytes))
		return val
	})
	ps.vm.Set("log", func(call otto.FunctionCall) otto.Value {
		entry := logger.Info().Str("script", ps.fileName)
		if len(call.ArgumentList) == 1 {
			arg := call.Argument(0)
			if arg.IsString() {
				entry.Msg(arg.String())
			} else {
				// TODO: doesn't work
				raw, err := arg.Export()
				if err != nil {
					logger.Error().Err(err).Msg("script log() called with invalid object")
				}
				entry.Fields(raw).Msg("script log")
			}
			return otto.UndefinedValue()
		}
		msg := call.Argument(0)
		if !msg.IsString() {
			logger.Error().Msg("script log() called with non-string first argument")
		}
		if len(call.ArgumentList) > 2 {
			entry := entry
			for i, arg := range call.ArgumentList[2:] {
				entry = entry.Str(strconv.Itoa(i), arg.String())
			}
			entry.Msg(msg.String())
		} else {
			// TODO: doesn't work
			raw, err := call.Argument(1).Export()
			if err != nil {
				logger.Error().Err(err).Msg("script log() called with invalid object")
			}
			entry.Fields(raw).Msg(msg.String())
		}
		return otto.UndefinedValue()
	})
}
