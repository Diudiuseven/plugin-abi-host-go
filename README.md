# plugin-abi-host-go

本地运行的能力宿主：在**不重启服务进程**的前提下，装载第三方以编译产物
（Go `-buildmode=plugin` 的 `.so`）提供的扩展模块。宿主与模块之间的接口
契约带版本代次（generation），装载前完成全部校验，单模块故障可隔离，装载
结果可持久化并在重启后完整恢复。

## 1. 接口代次语义

- 每个模块通过版本中立的元数据符号 `ABIMetadata func() string` 返回一份 JSON
  清单，其中必须声明自己**编译时使用的接口代次** `generation`。
- 宿主支持的代次区间是 `[host.MinGeneration, host.MaxGeneration]`（当前
  为 1–2）。默认策略 `GenerationReject`：低于下界或高于上界的模块在
  **装载之前**就被拒绝（错误码 `generation_too_old` /
  `generation_too_new`），绝不允许先装载、调用时才崩溃。
- 不同代次在同一进程内可以**同时存在**。模块标识是 `(id, generation)`，
  宿主为每个代次绑定对应的 Go 符号类型并做参数适配，因此每代模块得到的
  始终是与自身代次一致的语义：

  | 代次 | 入口符号契约 | 调用语义 |
  | --- | --- | --- |
  | 1 | `func(string) string` | 字符串进、字符串出 |
  | 2 | `func(map[string]any) (map[string]any, error)` | 结构化输入输出，可返回错误 |

- 代次之间不共享符号表：每个 `.so` 是独立 module 的独立产物，入口符号只
  在本模块内解析，不存在同名符号互相覆盖或全局状态串扰。宿主仅使用
  Go 标准库类型作为跨边界类型，避免宿主/模块类型身份不一致。

## 2. 模块清单与显式入口

`ABIMetadata` 返回的清单（manifest schema 版本固定为 `1`）：

```json
{
  "schema": 1,
  "id": "g1",
  "version": "1.0.0",
  "generation": 1,
  "requires": ["log"],
  "entries": [ { "name": "Echo", "kind": "call" } ]
}
```

- `entries` 是**显式入口声明**，宿主只通过清单里声明的名字与模块交互。
- 装载前逐项校验：清单非法 JSON / 错误 schema / 空 id、入口重名
  （`duplicate_entry`）、声明了但产物未导出（`missing_entry`）、符号
  存在但 Go 类型与该代次契约不符（`entry_type_mismatch`）。
- 所有拒绝都返回带模块 id（以及入口名）的 `*host.LoadError`，错误可定位
  到具体模块与入口；判定是「宿主能力 + 产物」的纯函数，相同输入结论
  可复现。
- `requires` 声明模块依赖的宿主能力；能力缺失时以 `missing_capability`
  在装载前拒绝。

## 3. 装载流程

```
Load(path)
  1. 计算产物 sha256
  2. plugin.Open，查找 ABIMetadata（任意 panic 被捕获 -> module_panic）
  3. 解析并校验清单（schema/id/入口重名）
  4. 校验代次区间（默认越界即拒绝）
  5. 校验 requires 的宿主能力全部存在
  6. 逐个入口解析符号并按代次校验 Go 类型
  7. 去重：(id, generation)
       - 同路径同 checksum：幂等返回已有实例，不产生重复实例
       - 不同产物：duplicate_existing 拒绝，旧实例不动
  8. 在旧快照之外构建完整的新集合（候选集）
  9. 配置了 StatePath：先把新集合原子落盘（临时文件 + fsync + rename）
 10. 原子发布新集合
```

任何一步失败都中止提交：内存中仍是上一份完整集合，磁盘上仍是上一份
完整状态文件，不存在集合级或入口级的半装载状态。

## 4. 并发与一致性

- 查询（`Modules` / `Module`）与调用（`Call`）走不可变快照的读锁，
  不阻塞装载；装载串行化并整体替换快照。
- 任一时刻外部读到的都是**某个已提交的完整集合**：快照 map 在发布后
  不再被修改，读者要么看到旧集合、要么看到新集合，不会看到混合状态。

## 5. 故障隔离与恢复

- 装载期或入口执行期的 `panic` 都在宿主侧被 `recover` 转成 `error`，
  不会击垮宿主或其他模块。
- 模块返回错误或 panic 计入该模块失败数；达到阈值
  （`Config.FailureThreshold`，默认 3）后模块被**隔离**，后续调用快速
  返回 `ErrQuarantined`。
- 隔离状态、累计失败数、最近失败原因、恢复方式都可通过
  `Module(id, generation)` 查询。
- 调用方错误（未知入口、参数类型不符合代次契约）不计入模块失败数。
- `Recover(id, generation)` 将模块恢复为健康并清零计数。

## 6. 重启恢复

配置 `StatePath` 后，每次提交的集合以 JSON 原子写入（临时文件 +
fsync + rename），内容包含每个模块的路径、身份与产物 sha256。

重启时 `New` 执行**全量重放**：逐个重新校验产物可读、checksum 未漂移、
清单与身份仍然匹配。重放是 all-or-nothing：任何一个模块不满足条件，
`New` 直接返回 `state_replay_failed` 错误，宿主保持空集合启动，由调用方
决定是修复产物还是删除状态文件重新装载，绝不会新旧两份混用。没有状态
文件则视为首次空启动。

## 7. 使用示例

```go
h, err := host.New(host.Config{
    Capabilities:     []string{"log", "metrics"},
    FailureThreshold: 3,
    StatePath:        "/var/lib/host/state.json", // 可选
})
if err != nil { return err } // 恢复旧集合失败

if _, err := h.Load("/opt/modules/g1.so"); err != nil {
    var le *host.LoadError
    if errors.As(err, &le) {
        log.Printf("rejected module=%s entry=%s code=%s: %s",
            le.ModuleID, le.Entry, le.Code, le.Detail)
    }
    return err
}

out, err := h.Call("g1", 1, "Echo", "hello") // 按代次 1 的语义调用
```

模块产物的编译方式：每个模块是一个独立的 `package main` 小 module，
只用 Go 标准库，编译命令：

```
go build -buildmode=plugin -buildvcs=false -o g1.so .
```

> 注：Go 插件要求宿主与产物使用同一 Go 工具链/标准库构建，且插件产物
> 仅支持 Linux/macOS 等支持 `-buildmode=plugin` 的平台；本项目在
> linux/amd64 上开发与测试。

## 8. 自动化测试

`host/testdata/plugins/` 下是各场景的真实插件源码（每个夹具是独立小
module），测试时由 `build_test.go` 统一编译为 `.so`。覆盖：

- 代次越界（过旧/过新）拒绝
- 入口缺失、入口符号类型不符、清单内入口重名
- 清单非法、缺少 `ABIMetadata`、依赖能力缺失
- 装载期 panic 被捕获、调用期 panic/错误隔离、阈值隔离、状态可查询、
  手动恢复
- 调用方错误不计入模块失败
- 重复装载幂等、同身份不同产物冲突拒绝
- 并发装载/查询/调用下集合始终一致、失败计数精确
- 重启（以子进程真实重执行测试二进制）后完整沿用旧集合、产物被篡改或
  缺失时拒绝重放、失败提交不改写旧状态文件

运行：

```
go test ./...              # 功能与集成测试（会编译插件，约数秒）
go test ./... -race        # 宿主同步逻辑的竞态检测
```

> `go test -race` 下测试二进制与插件产物之间存在 Go 工具链的包标识
> 差异（仅影响测试框架，不影响普通 `-race` 程序，见 README 的平台
> 说明）；因此插件集成测试默认常规构建，宿主自身的快照/计数同步由
> `TestRaceSnapshotSwapsAndCalls` 在 `-race` 下覆盖。
