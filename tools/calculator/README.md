# tools/calculator

## Role

提供无网络依赖的确定性算术工具。支持括号、加减乘除和小数；拒绝非法字符、除零与非有限结果。

## API / Methods

- `New() *Tool`
- `Spec() toolv1.ToolSpec`
- `Evaluate(string) (float64, error)`
- `Tool.Invoke` 实现 `tooling.Handler`
- `Adapter.Adapt` 实现 slash 文本到 JSON 参数映射

## Boundaries

不解析变量、函数或任意代码，不访问网络和文件，不写 Session 事件。

## Verification

```powershell
go test -count=1 ./tools/calculator
```
