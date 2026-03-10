// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package chromiumreceiver // import "github.com/stuart23/chromiumreceiver"

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"go.uber.org/zap"
)

// PerformanceMetric represents a single performance metric from CDP.
type PerformanceMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// PerformanceMetricsResult contains the metrics returned by Performance.getMetrics.
type PerformanceMetricsResult struct {
	Metrics []PerformanceMetric `json:"metrics"`
}

// TargetInfo represents information about a browser target.
type TargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// CDPClient wraps chromedp to provide CDP access for metric collection.
type CDPClient struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	browserCtx  context.Context
	browserStop context.CancelFunc
	logger      *zap.Logger
	endpoint    string

	// targetSessions caches chromedp contexts per target ID to avoid
	// detach/reattach cycles that can corrupt the browser session.
	targetSessions map[target.ID]targetSession
}

type targetSession struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewCDPClient creates a new CDP client that will connect to the given
// WebSocket debugger URL (e.g. ws://localhost:9222).
func NewCDPClient(endpoint string, logger *zap.Logger) *CDPClient {
	return &CDPClient{
		endpoint:       endpoint,
		logger:         logger,
		targetSessions: make(map[target.ID]targetSession),
	}
}

// Connect establishes a connection to the Chrome instance via CDP.
func (c *CDPClient) Connect(ctx context.Context) error {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, c.endpoint)
	c.allocCtx = allocCtx
	c.allocCancel = allocCancel

	browserCtx, browserStop := chromedp.NewContext(allocCtx)
	c.browserCtx = browserCtx
	c.browserStop = browserStop

	// Run an empty action to force the CDP connection to be established.
	if err := chromedp.Run(browserCtx); err != nil {
		allocCancel()
		return fmt.Errorf("failed to connect to CDP endpoint %s: %w", c.endpoint, err)
	}

	c.logger.Info("Connected to Chrome via CDP", zap.String("endpoint", c.endpoint))
	return nil
}

// Disconnect tears down the CDP connection.
func (c *CDPClient) Disconnect() error {
	for id, s := range c.targetSessions {
		s.cancel()
		delete(c.targetSessions, id)
	}
	if c.browserStop != nil {
		c.browserStop()
	}
	if c.allocCancel != nil {
		c.allocCancel()
	}
	return nil
}

// GetTargets returns all browser targets (pages, service workers, etc.).
func (c *CDPClient) GetTargets(ctx context.Context) ([]TargetInfo, error) {
	targets, err := chromedp.Targets(c.browserCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}

	infos := make([]TargetInfo, 0, len(targets))
	for _, t := range targets {
		infos = append(infos, TargetInfo{
			TargetID: string(t.TargetID),
			Type:     string(t.Type),
			Title:    t.Title,
			URL:      t.URL,
		})
	}
	return infos, nil
}

// getTargetCtx returns a cached chromedp context for the given target,
// creating one if it doesn't already exist.
func (c *CDPClient) getTargetCtx(targetID target.ID) context.Context {
	if s, ok := c.targetSessions[targetID]; ok {
		return s.ctx
	}
	ctx, cancel := chromedp.NewContext(c.browserCtx, chromedp.WithTargetID(targetID))
	c.targetSessions[targetID] = targetSession{ctx: ctx, cancel: cancel}
	return ctx
}

// GetPerformanceMetrics retrieves Performance.getMetrics from a specific
// target identified by targetID.
func (c *CDPClient) GetPerformanceMetrics(ctx context.Context, targetID target.ID) (*PerformanceMetricsResult, error) {
	targetCtx := c.getTargetCtx(targetID)

	var cdpMetrics []*performance.Metric
	if err := chromedp.Run(targetCtx,
		performance.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cdpMetrics, err = performance.GetMetrics().Do(ctx)
			return err
		}),
	); err != nil {
		delete(c.targetSessions, targetID)
		return nil, fmt.Errorf("failed to get performance metrics for target %s: %w", targetID, err)
	}

	result := &PerformanceMetricsResult{
		Metrics: make([]PerformanceMetric, 0, len(cdpMetrics)),
	}
	for _, m := range cdpMetrics {
		result.Metrics = append(result.Metrics, PerformanceMetric{
			Name:  m.Name,
			Value: m.Value,
		})
	}
	return result, nil
}
