// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The sandbox json module (plugin scripts) and the Lua<->JSON
// conversions shared with the MCP server (mcp.go). The depth/node caps
// bound a runaway table in both directions: a cyclic Lua table hits
// the depth cap instead of a stack overflow, a fan-out table cannot
// build a JSON bomb, a deep response cannot hang the conversion.

package app

import (
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

const (
	luaJSONMaxDepth = 8 // a cyclic table hits the depth cap, not a stack overflow
	luaJSONMaxNodes = 10000
)

// luaJSONModule builds the json table: encode(v) -> string, decode(s)
// -> value (nil, err on failure).
func luaJSONModule(vm *lua.LState) *lua.LTable {
	tbl := vm.NewTable()
	tbl.RawSetString("encode", vm.NewFunction(func(L *lua.LState) int {
		nodes := 0
		v, err := luaToJSON(L, L.Get(1), 0, &nodes)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		b, err := json.Marshal(v)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(b))
		return 1
	}))
	tbl.RawSetString("decode", vm.NewFunction(func(L *lua.LState) int {
		var v any
		if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(luaValue(L, v, 0))
		return 1
	}))
	return tbl
}

// luaValue converts a JSON-decoded arg value to a Lua value
// (recursive, depth-capped); the result is the args table for the
// chunk.
func luaValue(vm *lua.LState, v any, depth int) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(x)
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case []any:
		if depth >= luaJSONMaxDepth {
			return lua.LNil
		}
		t := vm.NewTable()
		for _, e := range x {
			t.Append(luaValue(vm, e, depth+1))
		}
		return t
	case map[string]any:
		if depth >= luaJSONMaxDepth {
			return lua.LNil
		}
		t := vm.NewTable()
		for k, val := range x {
			t.RawSetString(k, luaValue(vm, val, depth+1))
		}
		return t
	}
	return lua.LNil
}

// luaToJSON converts a Lua value to a JSON-able Go value: array
// tables -> []any, map tables -> map[string]any, scalars pass
// through, nil -> nil. Depth- and fan-out-capped (the node counter is
// per call, so concurrent calls never share state) - a runaway chunk
// cannot build a JSON bomb; an overrun fails the call.
func luaToJSON(vm *lua.LState, v lua.LValue, depth int, nodes *int) (any, error) {
	if depth >= luaJSONMaxDepth {
		return nil, fmt.Errorf("lua result too deep (depth %d)", depth)
	}
	switch x := v.(type) {
	case *lua.LTable:
		if x.Len() > 0 && pureArray(x) {
			out := make([]any, 0, x.Len())
			for i := 1; i <= x.Len(); i++ {
				if *nodes++; *nodes > luaJSONMaxNodes {
					return nil, fmt.Errorf("lua result too large")
				}
				e, err := luaToJSON(vm, x.RawGetInt(i), depth+1, nodes)
				if err != nil {
					return nil, err
				}
				out = append(out, e)
			}
			return out, nil
		}
		out := map[string]any{}
		var convErr error
		x.ForEach(func(k, val lua.LValue) {
			if convErr != nil {
				return
			}
			if *nodes++; *nodes > luaJSONMaxNodes {
				convErr = fmt.Errorf("lua result too large")
				return
			}
			e, err := luaToJSON(vm, val, depth+1, nodes)
			if err != nil {
				convErr = err
				return
			}
			out[k.String()] = e
		})
		return out, convErr
	case lua.LString:
		return string(x), nil
	case lua.LNumber:
		return float64(x), nil
	case lua.LBool:
		return bool(x), nil
	}
	return nil, nil
}

// pureArray reports whether the table's keys are exactly 1..n (a pure
// array); an empty table is a map by default.
func pureArray(t *lua.LTable) bool {
	n := t.Len()
	valid := true
	t.ForEach(func(k, _ lua.LValue) {
		ki, ok := k.(lua.LNumber)
		if !ok || float64(ki) < 1 || float64(ki) > float64(n) {
			valid = false
		}
	})
	return valid
}
