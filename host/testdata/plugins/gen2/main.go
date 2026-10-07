// Test plugin: Gen2 strict module.
package main

import (
	"context"
	"fmt"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
	"github.com/Diudiuseven/plugin-abi-host-go/plugin"
)

func main() {
	m := plugin.New(pluginhost.Manifest{
		ModuleID:     "gen2echo",
		Version:      "2.0.0",
		Generation:   pluginhost.Gen2,
		Capabilities: []string{"logging"},
		Entries: []pluginhost.EntrySpec{
			{Name: "echo", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
		},
	})
	m.Handle("echo", func(_ context.Context, args []any) (any, error) {
		return fmt.Sprintf("gen2:%v", args[0]), nil
	})
	_ = m.Serve()
}
