# tools/readdocs

## Role

在注入的允许根目录内读取文档片段，支持默认行范围和符号链接越界检查。

## API / Methods

- `New(map[string]string) (*Tool, error)`
- `Spec() toolv1.ToolSpec`
- `Tool.Invoke` 实现 `tooling.Handler`
- `Adapter.Adapt` 实现 slash 路径映射

## Boundaries

只能读文件，不能写文件、访问未登记根或跟随越界符号链接；不写 Session 事件。

## Verification

```powershell
go test -count=1 ./tools/readdocs
```
