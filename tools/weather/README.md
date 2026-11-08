# tools/weather

## Role

调用账户绑定的天气 HTTP provider，归一化成功结果以及超时、鉴权、额度和上游失败。

## API / Methods

- `New(ConfigResolver, *http.Client) *Tool`
- `ResolverFunc`
- `Spec() toolv1.ToolSpec`
- `Tool.Invoke` 实现 `tooling.Handler`
- `Adapter.Adapt` 实现 `location [date]` 直调

## Boundaries

不保存 secret，不直接读取 AccountStore；HTTP 响应限制大小且 provider 自由文本不进入稳定错误码。

## Verification

```powershell
go test -count=1 ./tools/weather
```
