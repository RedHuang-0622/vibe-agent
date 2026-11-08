# pkg/contracts/credential/v1

## Role

定义 Hub 与 Sessiond 之间的短期凭据 lease 契约。Sessiond 通过 lease 获得一次 provider 调用所需的 secret，不读取账户持久化文件。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `Broker.Issue` | interface method | 为已解析 Account binding 签发短期 lease |
| `Broker.Redeem` | interface method | 按 lease/invocation ID 兑换 secret |

主要 DTO：`LeaseRequest`、`Lease`、`RedeemRequest`、`RedeemResponse`。

## Boundaries

- `RedeemResponse.Secret` 只能存在于调用期内存。
- 不得写日志、事件、trace、TUI 或持久化文件。
- lease 必须绑定 Account revision、capability、purpose、TTL 和 invocation。

## Verification

```text
go test ./pkg/contracts/credential/v1
```
