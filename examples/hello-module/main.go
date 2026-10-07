// Example extension module: build it as a plain executable.
//
//	go build -o /tmp/hello-module ./examples/hello-module
package main

import (
	"context"
	"fmt"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
	"github.com/Diudiuseven/plugin-abi-host-go/plugin"
)

func main() {
	mod := plugin.New(pluginhost.Manifest{
		ModuleID:   "hello",
		Version:    "1.0.0",
		Generation: pluginhost.Gen2,
		Entries: []pluginhost.EntrySpec{
			{Name: "greet", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
		},
	})
	mod.Handle("greet", func(_ context.Context, args []any) (any, error) {
		return fmt.Sprintf("hello, %s!", args[0]), nil
	})
	if err := mod.Serve(); err != nil {
		panic(err)
	}
}
