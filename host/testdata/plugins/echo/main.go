// Test plugin: Gen1 echo/count module.
package main

import (
	"context"
	"os"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
	"github.com/Diudiuseven/plugin-abi-host-go/plugin"
)

func main() {
	m := plugin.New(pluginhost.Manifest{
		ModuleID:   "echo",
		Version:    "1.0.0",
		Generation: pluginhost.Gen1,
		Entries: []pluginhost.EntrySpec{
			{Name: "echo", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
			{Name: "add", Input: []pluginhost.Kind{pluginhost.KindInt, pluginhost.KindInt}, Output: pluginhost.KindInt},
		},
	})
	m.Handle("echo", func(_ context.Context, args []any) (any, error) {
		return args[0], nil
	})
	m.Handle("add", func(_ context.Context, args []any) (any, error) {
		// Gen1 lenient semantics: args arrive normalized; JSON numbers decode
		// as float64 on the module side.
		return int64(args[0].(float64) + args[1].(float64)), nil
	})
	if err := m.Serve(); err != nil {
		os.Exit(1)
	}
}
