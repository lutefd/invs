package metadata

import (
	"context"
	"errors"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		m    Metrics
		want string
	}{{Metrics{}, "succeeded"}, {Metrics{Rejected: 1}, "partial"}, {Metrics{Err: errors.New("x")}, "failed"}, {Metrics{Err: errors.New("x"), RawPayloads: 1}, "partial"}, {Metrics{Err: context.Canceled}, "cancelled"}}
	for _, c := range cases {
		if got := classify(c.m); got != c.want {
			t.Fatalf("got %s want %s", got, c.want)
		}
	}
}
