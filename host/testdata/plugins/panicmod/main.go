// Test plugin: always errors/panics, and can crash on demand.
package main

import (
	"context"
	"os"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
	"github.com/Diudiuseven/plugin-abi-host-go/plugin"
)

func main() {
	m := plugin.New(pluginhost.Manifest{
		ModuleID:   "panicmod",
		Version:    "1.0.0",
		Generation: pluginhost.Gen1,
		Entries: []pluginhost.EntrySpec{
			{Name: "boom", Input: nil, Output: pluginhost.KindString},
			{Name: "crash", Input: nil, Output: pluginhost.KindString},
			{Name: "ok", Input: nil, Output: pluginhost.KindString},
		},
	})
	m.Handle("boom", func(_ context.Context, _ []any) (any, error) {
		panic("intentional panic")
	})
	m.Handle("crash", func(_ context.Context, _ []any) (any, error) {
		os.Exit(2)
		return nil, nil
	})
	m.Handle("ok", func(_ context.Context, _ []any) (any, error) {
		return "fine", nil
	})
	_ = m.Serve()
}
