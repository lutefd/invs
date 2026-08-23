# B3 listed-company corporate-action evidence

- Date: 2026-08-23
- Scope: source-native B3 corporate-action evidence for one explicitly requested issuer code
- Status: **raw-first evidence adapter accepted; canonical corporate-action publication not admitted**

## Decision

B3's public listed-company application provides useful event evidence for an
explicit issuer/company code. The bounded adapter calls the listed-company
corporate-action endpoint, retains the complete JSON response before parsing,
and parses the cash-dividend, stock-action, and subscription sections with
their source labels, ISINs, date fields, decimal lexemes, and raw locators.

This is not yet a canonical `CorporateAction` admission. The checked endpoint
does not expose a provider event ID, an earliest public publication timestamp,
or a correction/revision number. `approvedOn` is retained as B3's decision or
approval date; it is not promoted to `published_at`. Receipt time would be an
installation-knowledge `available_at` only if a future collector publishes a
source-native evidence record. The adapter therefore does not publish price
adjustments or historical corporate-action rows.

## Source and request boundary

- [B3 listed-company corporate-actions page for Petrobras](https://sistemaswebb3-listados.b3.com.br/listedCompaniesPage/main/9512/PETR/corporate-actions?language=en-US)
- [B3 public regulatory download information](https://www.b3.com.br/pt_br/noticias/dados-para-download.htm)
- Listed-company API base used by the adapter:
  `https://sistemaswebb3-listados.b3.com.br/listedCompaniesProxy/CompanyCall/`

The request requires the B3 `issuingCompany` code explicitly (for example,
`PETR`) and accepts only `en-US` or `pt-BR`. It never derives an issuer code
from a ticker. That matters because one issuer can have several listed
securities and the current B3 instrument source is ticker-oriented.

## Live evidence

The opt-in acceptance command was:

```sh
INVS_B3_LIVE=1 \
B3_ACTION_COMPANY=PETR \
go test ./internal/providers/b3 -run '^TestLiveListedCompanyCorporateActions$' -count=1 -v
```

It passed against the real B3 endpoint and returned:

| Field | Observed value |
| --- | --- |
| Issuer code | `PETR` |
| CVM code | `9512` |
| Cash-dividend rows | `24` |
| Stock-action rows | `6` |
| Subscription rows | `1` |
| Response bytes | `7,148` |
| Response SHA-256 | `0079872ff54dcb99578738fb59cb6b57bc59e3aefa2231aa0b1f91378f36d7ec` |

The response included ISIN-linked rows such as dividend rates/payment dates,
stock-action factors, and subscription percentage/price/trading-period data.
The source also returned older action dates, including a 1974 subscription and
1994 stock action, but the endpoint response itself does not establish a
complete or revisioned historical archive. A row's presence is evidence of what
this endpoint returned at receipt, not proof that the row was publicly
available at its approval date.

## Adapter and retention contract

- `internal/providers/b3/corporate_actions.go` owns the HTTP and source parsing.
- The full response is returned as one `application/json` raw resource before
  parse errors are returned.
- Source decimal fields remain strings (`rate`, `factor`, `percentage`, and
  `priceUnit`) so no binary floating-point rounding is introduced.
- `approvedOn`, `lastDatePrior`, `paymentDate`, and `subscriptionDate` are
  parsed as UTC civil dates. B3 sentinel dates such as `12/31/9999` and
  `01/01/1900` become absent typed values while the original JSON remains in
  the raw resource.
- Each accepted row requires a valid B3 ISIN. Source action labels remain
  explicit (`DIVIDENDO`, `DESDOBRAMENTO`, `SUBSCRICAO`, and so on).
- The source-native result is intentionally separate from the existing
  provider-neutral corporate-action schema.

## Admission checklist

| Requirement | Result | Boundary |
| --- | --- | --- |
| Stable security linkage | Pass for returned rows | B3 ISIN is required and retained |
| Effective/event dates | Partial | Approval, last-date-prior, payment, and subscription dates are present where supplied |
| Amount/ratio precision | Pass | Decimal lexemes are retained as strings |
| Raw retention/replay | Pass | Complete JSON, SHA-256, request metadata, and row locators are retained |
| Public publication timestamp | Fail | No earliest-public-availability field is exposed |
| Provider event identity | Fail | No event ID is exposed; a deterministic locator is not a vendor revision ID |
| Correction/revision chronology | Fail | No correction revision or version sequence is exposed |
| Canonical adjustment publication | Blocked | Requires an explicit policy for missing publication/revision semantics |

## Consequence

The B3 lifecycle/corporate-action gate remains open. This slice improves the
evidence boundary without turning a current web display into point-in-time
truth. B3's documented UP2DATA product now has a separate sample-layout
acceptance and transport/access gate in the [UP2DATA corporate-action evidence
report](2026-08-23-b3-up2data-corporate-actions-evidence.md). The next safe work
is authorized access to that product (or a public versioned alternative), then
an explicit availability and correction policy before canonical adjustments are
published.
