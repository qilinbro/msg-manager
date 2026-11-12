// Package agent produces bounded, evidence-linked model suggestions. It never
// receives platform credentials or exposes a tool for sending a message.
package agent

import "context"

type Provider interface {
	Analyze(context.Context, Input) (Result, error)
}
