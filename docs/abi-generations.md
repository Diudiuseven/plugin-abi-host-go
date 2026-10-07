# 接口代次（Generation）语义

## 核心概念

- **Generation（代次）**：宿主与模块之间接口契约的不透明整数编号，随接口语义演进而单调递增。当前 SDK：`Gen1 = 1`、`Gen2 = 2`。
- **Protocol version（帧版本）**：stdio 行分隔 JSON 的传输/分帧版本（`ProtocolVersion = 1`）。它与 Generation 相互独立——分帧几乎不变，语义靠代次演进。
- 模块在清单（Manifest）中声明自己**编译时使用的代次**，进程启动后的 Banner 必须与清单完全一致，否则装载被拒绝。

## 同一进程内的多代次共存

每个模块是独立的子进程，宿主为每个模块**钉住它自己声明的代次**：

- 调用参数按该模块代次的规则校验和归一化；
- 返回值按该模块代次的规则检查；
- 模块之间没有共享内存、没有同名符号、没有全局状态，因此不同代次互不污染。

同一模块标识（`module_id`）允许以不同代次同时装载；相同标识**且相同代次**重复装载会被判定为 `duplicate_instance` 而拒绝（`id@gen` 是唯一实例键）。

## 代次语义差异

| 维度 | Gen1（遗留，宽松） | Gen2（严格） |
| --- | --- | --- |
| 参数个数 | 多余参数丢弃；缺失参数补零值 | 必须精确匹配，否则调用被拒绝（不计模块失败） |
| 参数类型 | 在位参数类型不符时以零值兜底 | 必须精确匹配，否则拒绝调用 |
| 返回值 | 原样返回 | 必须与声明的 Output kind 一致；违反视为模块故障，计入隔离计数 |
| 零值规则 | string→`""`，int→`0`，bool→`false` | — |

零值兜底是 Gen1 已发布的历史行为，宿主不会改动已装载 Gen1 模块的语义；新契约一律从 Gen2 的严格规则开始。

## 兼容策略 `Support.Policy`

宿主通过 `host.Support` 声明：

- `HostGeneration`：当前宿主代次（本 SDK 为 Gen2）；
- `MinGeneration` / `MaxGeneration`：仍保留原生支持的代次范围；
- `Capabilities`：宿主提供的可选能力集合；
- `Policy`：
  - `PolicyReject`：模块代次必须恰好等于宿主代次，否则 `unsupported_generation`；
  - `PolicyAllowLegacy`（默认）：允许 `[Min, Max]` 内的较旧代次；高于 `Max`（宿主看不懂）一律拒绝。

模块还可以在清单里声明自己能配合的宿主代次区间 `min_host_generation` / `max_host_generation`；当前宿主落不进该区间时，以 `generation_range_unsatisfied` 拒绝。

所有代次判定都发生在**装载之前的纯校验阶段**，相同输入结果可复现，绝不会出现“装载成功、调用时才因 ABI 问题崩溃”。

## 可选能力

清单 `capabilities` 列出模块依赖的宿主能力；宿主 `Support.Capabilities` 中缺任一能力即 `capability_missing` 拒绝。当前内置能力：`logging`。
