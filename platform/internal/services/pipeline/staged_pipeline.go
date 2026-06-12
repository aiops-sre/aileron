package pipeline

// staged_pipeline.go
//
// StagedPipeline implements a multi-stage alert processing pipeline with
// separate worker pools per stage and graceful backpressure. Critical alerts
// bypass the fast path and go directly to the topo stage.
//
// Stage 1 — FAST PATH  (32 workers, cap 10000): fingerprint dedup, resolved forwarding
// Stage 2 — TOPO PATH  (16 workers, cap 5000):  root cause engine, Redis graph
// Stage 3 — FULL PATH  (8 workers, cap 2000):   parallel 4-strategy correlation

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

// Processor interfaces allow the three stages to be implemented separately
// and wired in from cmd/main.go without circular imports.

// FastPathProcessor handles dedup and resolved-alert fast-exit.
// Returns forwarded=true when the alert needs further processing.
type FastPathProcessor interface {
	Process(ctx context.Context, alert *models.Alert) (forwarded bool, err error)
}

// TopoPathProcessor runs the root-cause engine (Redis graph, deterministic).
// Returns matched=true when the alert was handled (attached to or created an incident).
type TopoPathProcessor interface {
	Process(ctx context.Context, alert *models.Alert) (matched bool, err error)
}

// FullCorrelationProcessor runs the 4-strategy parallel scoring pipeline.
type FullCorrelationProcessor interface {
	Process(ctx context.Context, alert *models.Alert) error
}

// StagedPipeline routes alerts through three processing stages with bounded
// concurrency per stage and backpressure via non-blocking channel sends.
type StagedPipeline struct {
	fastCh chan *models.Alert
	topoCh chan *models.Alert
	fullCh chan *models.Alert

	fastWorkers int
	topoWorkers int
	fullWorkers int

	fastProcessor FastPathProcessor
	topoProcessor TopoPathProcessor
	fullProcessor FullCorrelationProcessor

	// Metrics — all updated atomically
	processed int64
	dropped   int64
	fastHits  int64
	topoHits  int64
	fullHits  int64

	wg sync.WaitGroup
}

// NewStagedPipeline creates the pipeline. Wire processors from cmd/main.go.
func NewStagedPipeline(fast FastPathProcessor, topo TopoPathProcessor, full FullCorrelationProcessor) *StagedPipeline {
	return &StagedPipeline{
		fastCh:        make(chan *models.Alert, 10000),
		topoCh:        make(chan *models.Alert, 5000),
		fullCh:        make(chan *models.Alert, 2000),
		fastWorkers:   32,
		topoWorkers:   16,
		fullWorkers:   8,
		fastProcessor: fast,
		topoProcessor: topo,
		fullProcessor: full,
	}
}

// Start launches all worker goroutines. Call once, pass the same ctx to Stop.
func (s *StagedPipeline) Start(ctx context.Context) {
	for i := 0; i < s.fastWorkers; i++ {
		s.wg.Add(1)
		go s.runFastWorker(ctx)
	}
	for i := 0; i < s.topoWorkers; i++ {
		s.wg.Add(1)
		go s.runTopoWorker(ctx)
	}
	for i := 0; i < s.fullWorkers; i++ {
		s.wg.Add(1)
		go s.runFullWorker(ctx)
	}
	go s.logMetrics(ctx)
}

// Enqueue submits an alert to the fast stage. Non-blocking.
// Critical alerts bypass directly to the topo stage on fast-path overflow.
func (s *StagedPipeline) Enqueue(alert *models.Alert) bool {
	select {
	case s.fastCh <- alert:
		return true
	default:
		if alert.Severity == "critical" {
			select {
			case s.topoCh <- alert:
				return true
			default:
			}
		}
		atomic.AddInt64(&s.dropped, 1)
		log.Printf("[StagedPipeline] DROPPED alert %s (severity=%s) — channels full", alert.ID, alert.Severity)
		return false
	}
}

// Wait blocks until all worker goroutines exit (after ctx cancellation).
func (s *StagedPipeline) Wait() {
	s.wg.Wait()
}

func (s *StagedPipeline) runFastWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.fastCh:
			forwarded, err := s.fastProcessor.Process(ctx, alert)
			if err != nil {
				log.Printf("[FastWorker] error on %s: %v — forwarding to full", alert.ID, err)
				s.enqueueToFull(alert)
				continue
			}
			if forwarded {
				s.enqueueToTopo(alert)
			} else {
				atomic.AddInt64(&s.fastHits, 1) // handled at fast stage (dedup/resolved)
			}
			atomic.AddInt64(&s.processed, 1)
		}
	}
}

func (s *StagedPipeline) runTopoWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.topoCh:
			matched, err := s.topoProcessor.Process(ctx, alert)
			if err != nil {
				s.enqueueToFull(alert)
				continue
			}
			if matched {
				atomic.AddInt64(&s.topoHits, 1)
			} else {
				s.enqueueToFull(alert)
			}
		}
	}
}

func (s *StagedPipeline) runFullWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.fullCh:
			if err := s.fullProcessor.Process(ctx, alert); err != nil {
				log.Printf("[FullWorker] error on %s: %v", alert.ID, err)
			}
			atomic.AddInt64(&s.fullHits, 1)
		}
	}
}

func (s *StagedPipeline) enqueueToTopo(alert *models.Alert) {
	select {
	case s.topoCh <- alert:
	default:
		s.enqueueToFull(alert)
	}
}

func (s *StagedPipeline) enqueueToFull(alert *models.Alert) {
	select {
	case s.fullCh <- alert:
	default:
		log.Printf("[StagedPipeline] full channel overflow — dropping alert %s", alert.ID)
		atomic.AddInt64(&s.dropped, 1)
	}
}

func (s *StagedPipeline) logMetrics(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()

	var lastProcessed, lastDropped int64

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			processed := atomic.LoadInt64(&s.processed)
			dropped := atomic.LoadInt64(&s.dropped)

			// Skip the log line entirely when nothing has moved since last tick.
			if processed == lastProcessed && dropped == lastDropped {
				continue
			}

			log.Printf("pipeline: processed=%d (+%d) dropped=%d fastHits=%d topoHits=%d fullHits=%d queues(f=%d t=%d x=%d)",
				processed, processed-lastProcessed, dropped,
				atomic.LoadInt64(&s.fastHits), atomic.LoadInt64(&s.topoHits), atomic.LoadInt64(&s.fullHits),
				len(s.fastCh), len(s.topoCh), len(s.fullCh))

			lastProcessed = processed
			lastDropped = dropped
		}
	}
}

// ClusterPartitionKey returns a Kafka partition key for an alert.
// Ensures all alerts from the same cluster are processed in order by the same consumer.
func ClusterPartitionKey(alert *models.Alert) string {
	if alert.Labels != nil {
		if cluster := alert.Labels["k8s.cluster.name"]; cluster != "" {
			return cluster
		}
		if host := alert.Labels["host.name"]; host != "" {
			if len(host) >= 3 {
				return host[:3]
			}
			return host
		}
	}
	return alert.Source
}
