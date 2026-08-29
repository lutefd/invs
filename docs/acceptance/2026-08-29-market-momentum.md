# Market-momentum feature-set implementation acceptance

- Date: 2026-08-29 (`America/Sao_Paulo`)
- Contract and registry: `dd450b3` (`feat(registry): register market momentum feature set`)
- Producer and operator dispatch: `941c6b5` (`feat(features): add market momentum producer`)
- Status: **accepted implementation slice; v0.3 exit gate remains open**

## Decision

The repository now has a second closed feature-set producer,
`market-momentum` 1.0.0. It consumes one point-in-time daily price series through
`ResearchCatalog.point_in_time_inputs` and publishes a strict manifest-backed
artifact with four observation-based close returns, annualized trailing volatility,
and trailing maximum drawdown. Its maximum warmup is 253 closes; shorter outputs
become available at their own prerequisites, while missing required closes are typed
nulls and zero-denominator or invalid price calculations reject the partition.

The feature set requires the same canonical exchange-calendar pin and
`after_close_next_session` decision clock as `market-basic`. Selected input manifest
and part hashes, the calendar pin, feature identity, and decision timestamp are
fingerprinted together. Artifact availability is the maximum selected input or
calendar availability plus the declared computation delay.

The price input remains `installation_replay_only` with
`conservative_receipt_time` availability. This acceptance does not upgrade source
fitness, claim historical public availability, or add a strategy, signal, backtest,
portfolio, execution, label, or model boundary.

## Exact outputs

| Output | Contract |
| --- | --- |
| `return_1m` | `close_t / close_(t-21) - 1` |
| `return_3m` | `close_t / close_(t-63) - 1` |
| `return_6m` | `close_t / close_(t-126) - 1` |
| `return_12m` | `close_t / close_(t-252) - 1` |
| `realized_volatility_1m` | Annualized sample standard deviation of the trailing 21 one-observation returns, using `sqrt(252)` |
| `max_drawdown_1m` | Minimum running `close / prior_peak - 1` across the trailing 21 eligible closes |

All values are canonical decimal strings or null. Calculations use a fixed high-
precision Decimal context. The strict contracts are
[`feature-momentum-manifest.schema.json`](../../schemas/feature-momentum-manifest.schema.json)
and
[`feature-momentum-observation.schema.json`](../../schemas/feature-momentum-observation.schema.json).

## Acceptance evidence

The focused container suite passed:

```text
15 passed in 1.92s
All checks passed!
```

It uses manifest-backed daily Parquet fixtures and independently verifies:

- exact Decimal calculations for all six outputs over 253 closes;
- 21/22/64-observation warmup boundaries for drawdown, volatility, and return
  horizons;
- zero-denominator rejection;
- idempotent publication and output-part tamper rejection;
- generic reader dispatch and direct CLI dispatch; and
- dataset-level batch dispatch through the checked-in registry.

The full repository ladder then passed at the producer commit:

| Check | Result |
| --- | --- |
| `go test ./...` | passed |
| `go vet ./...` | passed |
| `python3 schemas/validate_schemas.py` | `validated 24 JSON Schema documents` |
| `python -m pytest` | `100 passed` |
| `python -m ruff check research tests` | passed |

## Operator boundary

The single-artifact target accepts:

```sh
make feature \
  SECURITY_ID=<security-uuid> \
  DECISION_AT=<canonical-utc-timestamp> \
  CALENDAR_PIN=/absolute/path/calendar-pin.json \
  FEATURE_SET=market-momentum \
  FEATURE_SET_VERSION=1.0.0
```

The dataset-level target accepts the same identity through
`FEATURE_SET=market-momentum FEATURE_SET_VERSION=1.0.0`. Both paths validate child
artifacts before returning a manifest. Existing `market-basic` artifacts remain
readable through the legacy strict contract.

## Remaining boundary

Feature-level null-reason and stale-input reporting, broader fundamental and macro
families, clean-root multi-asset reproduction, and the complete v0.3 exit scenario
remain open. The next narrow unit is feature-level coverage/null reporting; receipt-
time prices must remain installation-replay only.
