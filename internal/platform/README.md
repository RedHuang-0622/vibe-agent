# internal/platform

## Role

OS 差异适配层：跨平台独占文件锁和当前用户登录启动项。

## API / Methods

导出 `AcquireFileLock`、`FileLock`、`InstallAutostart`、
`UninstallAutostart`、`AutostartStatus` 和 `StartAutostartPath`。

## Boundaries

- 平台判断不泄漏进 domain/application。
- 不包含 Account 业务规则。
- 启动项只能拉起 Hub，不能直接启动 Sessiond。
- Windows 会话锁通过独占共享模式文件句柄实现；Unix 使用 `flock`。

## Verification

```text
go test -count=1 ./internal/platform/...
```
