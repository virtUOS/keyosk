package handlers

import (
	"context"
	"log/slog"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// usageWindowDays is how far back the dashboard reports unless configured
// otherwise (USAGE_HISTORY_DAYS). Long enough to cover a month's key
// validity.
const usageWindowDays = 30

// usageReader reads a person's usage from the gateway, live on every page
// load.
//
// Nothing is cached. Most people come to the dashboard rarely, and the one
// who loads it again within a minute has usually just sent a request and
// wants to see it, which a cache would hide. One read of the daily activity
// per page view is cheap: a month of use fits on one page of results. With
// nothing shared, one person's slow read holds up nobody else's page, and the
// history and the windows the card shows beside it are read at the same
// moment, so they cannot disagree.
type usageReader struct {
	reporter keyprovider.UsageReporter
	// windowDays is how far back History reaches.
	windowDays int
}

func newUsageReader(r keyprovider.UsageReporter) *usageReader {
	return &usageReader{reporter: r, windowDays: usageWindowDays}
}

// History returns the per-day and per-model usage of a person.
//
// Both halves come from one upstream read, so they can never disagree about
// the window they cover. A failed read returns an empty History with the
// error, and the dashboard says so instead of showing a silently shorter
// report.
func (ur *usageReader) History(ctx context.Context, ownerID string) (keyprovider.History, error) {
	if ur.reporter == nil || ownerID == "" {
		return keyprovider.History{}, nil
	}
	h, err := ur.reporter.History(ctx, ownerID, ur.windowDays)
	if err != nil {
		slog.Error("read usage history", "err", err)
		return keyprovider.History{}, err
	}
	return h, nil
}

// Windows reports consumption against every allowance window of the key and
// its owner.
func (ur *usageReader) Windows(ctx context.Context, ref, ownerID string) ([]keyprovider.WindowUsage, error) {
	if ur.reporter == nil || ref == "" {
		return nil, nil
	}
	return ur.reporter.Windows(ctx, ref, ownerID)
}
