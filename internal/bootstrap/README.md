# internal/bootstrap

## Role

Composition root 与共享运行路径。显式装配 Hub 的 Account、Knowledge、
Session 管理器和 control API；解析数据目录、Hub 状态、二进制路径并实现幂等启动。

## API / Methods

导出 `RunHub`、`EnsureHub`、`HubClient`、`HubExecutablePath`、
`ResolvePaths`、`Paths`、`HubState`、`LoadState` 和 `SaveState`。

## Boundaries

- 唯一允许知道具体 adapter 的层。
- 不包含业务规则。
- Hub 装配 Account Admin；Sessiond 装配 Account Reader。
- 不使用包级可变全局状态；运行依赖通过构造参数/返回对象传递。

## Verification

```text
go test -count=1 ./internal/bootstrap/...
```
