# plugin-abi-host-go

本地运行的能力宿主：服务不重启即可装载第三方以**编译产物（可执行文件）**提供的扩展模块；接口契约版本化，模块故障进程级隔离。

- 模块以子进程运行，stdio 上行分隔 JSON 通信（无 cgo、无 `.so`、跨平台）；
- 接口代次（Gen1/Gen2）在清单与 Banner 中显式声明，装载前完成全部兼容性校验；
- 多代次可在同一进程的宿主中共存，各模块严格按自身代次语义服务，互不污染；
- 入口必须显式声明，缺失/重名/签名不符在装载前拒绝，错误定位到模块与入口；
- panic、错误返回、进程崩溃累计失败计数，阈值后自动隔离，可查询原因并 `Recover`；
- 装载/查询/调用并发安全，原子快照保证对外永远是某个完整集合，相同 `id@gen` 不产生重复实例；
- 全有或全无的装载事务 + 单文件原子持久化，重启后完整恢复上一份集合。

## 模块作者

```go
mod := plugin.New(pluginhost.Manifest{
    ModuleID: "hello", Version: "1.0.0", Generation: pluginhost.Gen2,
    Entries: []pluginhost.EntrySpec{
        {Name: "greet", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
    },
})
mod.Handle("greet", func(ctx context.Context, args []any) (any, error) {
    return "hello, " + args[0].(string) + "!", nil
})
if err := mod.Serve(); err != nil { panic(err) }
```

`go build` 成可执行文件，连同 JSON 清单交付。

## 宿主

```go
h, err := host.Open(ctx,
    host.WithSupport(host.DefaultSupport()),
    host.WithPersistence(host.NewFileStore("./state")),
)
snap, err := h.LoadSet(ctx, []pluginhost.Manifest{manifest})
v, err := h.Call(ctx, "hello", "greet", "world")
for _, mi := range h.Query() { fmt.Println(mi.Key, mi.State, mi.Failure) }
```

## 文档

- [接口代次语义](docs/abi-generations.md)
- [装载流程与可靠性保证](docs/loading.md)

## 测试

```sh
go test -race ./...
```

测试覆盖：代次不匹配与宿主区间、入口缺失/重名/签名不符、能力缺失、
panic/崩溃隔离与恢复、成功清零、并发装载与调用、失败不留半装载、
重启恢复与拒绝事务不落盘、校验可复现。
