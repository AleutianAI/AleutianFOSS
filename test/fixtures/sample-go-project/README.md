# sample-go-project

Test fixture for `aleutian` CLI end-to-end validation.

## Purpose

This project is used by `cli_unit_test.go` and `test/e2e/cli_validation.sh` to validate
all CLI-01 commands against a real Go project with known properties.

## Known Properties

| Property | Value | Used By |
|----------|-------|---------|
| `auth.ValidateToken` has multiple callers | `main.HandleRequest`, `handler.HandleRequest` | `aleutian graph callers` |
| `db.Connect` called by main | `main.main` | `aleutian graph callees` |
| `config.APIKey` contains credential pattern | `sk-test-hardcoded-secret-12345` | `aleutian policy check` |
| All packages have call edges | Go module with imports | `aleutian init` |

## Call Graph

```
main.main
  ├─→ config.Load
  ├─→ db.Connect
  ├─→ db.Close
  ├─→ auth.Init
  └─→ handler.StartServer
           └─→ handler.HandleRequest
                    ├─→ auth.ValidateToken  ◄── also called by main.HandleRequest
                    └─→ db.Query

main.HandleRequest
  └─→ auth.ValidateToken

auth.Login
  └─→ auth.ValidateToken
```

## Policy Check

`config/config.go` contains an intentional credential pattern:
```go
APIKey: "sk-test-hardcoded-secret-12345",
```
This will produce at least one violation when scanned with `aleutian policy check`.
