# tools/search

## Role

通过注入的账户 binding 和 HTTP 客户端调用搜索 provider，并归一化鉴权、额度、限流、超时和依赖错误。

## API / Methods

- `New(ConfigResolver, *http.Client) *Tool`
- `ResolverFunc`
- `Spec() toolv1.ToolSpec`
- `Tool.Invoke` 实现 `tooling.Handler`
- `Adapter.Adapt` 实现 slash query 映射

## Boundaries

工具不读取 credential 文件，不记录 Authorization header、provider body 或原始 secret；真实调用必须由 Hub AccountReader 提供配置。

## Verification

```powershell
go test -count=1 ./tools/search
```
