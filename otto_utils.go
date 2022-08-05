package main

import (
	"fmt"
	"reflect"
	"runtime"

	"github.com/couchbase/gocbcore/v10/memd"
	"github.com/robertkrimen/otto"
	"golang.org/x/exp/constraints"
)

func GetFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

func objCast[T any](obj *otto.Object, key string, parser func(otto.Value) (T, error), undefinedOK bool) T {
	val, err := obj.Get(key)
	if err != nil {
		panic(err)
	}
	if val.IsUndefined() {
		if undefinedOK {
			var zero T
			return zero
		}
		panic("undefined")
	}
	retval, err := parser(val)
	if err != nil {
		var zero T
		panic(fmt.Errorf("error processing key %q with parser %s (type %T): %v", key, GetFunctionName(parser), zero, err))
	}
	return retval
}

func parseMagic(val otto.Value) (memd.CmdMagic, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return memd.CmdMagic(intval), nil
}

func parseCmd(val otto.Value) (memd.CmdCode, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return memd.CmdCode(intval), nil
}

func parseU8(val otto.Value) (uint8, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return uint8(intval), nil
}

func parseU16(val otto.Value) (uint16, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return uint16(intval), nil
}

func parseU32(val otto.Value) (uint32, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return uint32(intval), nil
}

func parseU64(val otto.Value) (uint64, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return uint64(intval), nil
}

func parseStatus(val otto.Value) (memd.StatusCode, error) {
	intval, err := val.ToInteger()
	if err != nil {
		return 0, err
	}
	return memd.StatusCode(intval), nil
}

func parseByteArray(val otto.Value) ([]byte, error) {
	rawVal, err := val.Export()
	if err != nil {
		return nil, err
	}
	switch typed := rawVal.(type) {
	case []byte:
		return typed, nil
	case []any:
		return castByteArray(typed)
	case []int64:
		return castNumericArrayToByteArray(typed)
	default:
		return nil, fmt.Errorf("parseByteArray: invalid type %T", rawVal)
	}
}

func castNumericArrayToByteArray[T constraints.Integer](arr []T) ([]byte, error) {
	ret := make([]byte, len(arr))
	for i := range arr {
		ret[i] = byte(arr[i])
	}
	return ret, nil
}

func castByteArray(arr []any) ([]byte, error) {
	ret := make([]byte, len(arr))
	for i := range arr {
		bv, ok := arr[i].(byte)
		if !ok {
			return nil, fmt.Errorf("castByteArray: arr[%d] (%#v) was %T, not byte", i, arr[i], arr[i])
		}
		ret[i] = bv
	}
	return ret, nil
}
