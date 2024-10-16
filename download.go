package main

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/VividCortex/ewma"
)

type Downloader struct {
	provider    Provider
	currentSize int64
	metrics     []TransferMetric
	done        bool
	mu          sync.RWMutex
	ewmas       []ewma.MovingAverage
}

func NewDownloader(provider Provider) *Downloader {
	ewmas := make([]ewma.MovingAverage, numConcurrent)
	for i := range ewmas {
		ewmas[i] = ewma.NewMovingAverage(5)
	}
	return &Downloader{
		provider: provider,
		metrics:  make([]TransferMetric, numConcurrent),
		ewmas:    ewmas,
	}
}

func (d *Downloader) OperationType() string {
	return "Download"
}

func (d *Downloader) Provider() Provider {
	return d.provider
}

func (d *Downloader) Start() {
	var wg sync.WaitGroup
	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			d.downloadWorker(id)
		}(i)
	}

	go func() {
		wg.Wait()
		d.mu.Lock()
		d.done = true
		d.mu.Unlock()
	}()
}

func (d *Downloader) downloadWorker(id int) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()

	downloadLimit := d.provider.DownloadLimit()
	downloaded := int64(0)

	for d.currentSize > downloaded {
		downloadSize := min(downloadLimit, d.currentSize-downloaded)

		metric := NewSpeedMeter(1 * time.Second)

		req, err := http.NewRequestWithContext(ctx, "GET", d.provider.DownloadURL(downloadSize), nil)
		if err != nil {
			d.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			d.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}
		defer resp.Body.Close()

		var current int64

		for {
			select {
			case <-ctx.Done():
				d.updateMetric(id, TransferMetric{Err: ctx.Err(), Done: true})
				return
			default:
				n, err := io.CopyN(io.Discard, resp.Body, 256*1024) // Read 256KiB at a time
				current += n
				metric.Add(n)
				progress := float64(current) / float64(d.currentSize)
				d.ewmas[id].Add(metric.Value())

				d.updateMetric(id, TransferMetric{
					Progress: progress,
					Speed:    d.ewmas[id].Value() * 8 / 1024 / 1024,
					Done:     progress >= 1,
				})

				if err != nil || progress >= 1 {
					if err != io.EOF {
						d.updateMetric(id, TransferMetric{Err: err, Done: true})
					}
					return
				}
			}
		}
	}
}

func (d *Downloader) updateMetric(id int, metric TransferMetric) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics[id] = metric
}

func (d *Downloader) GetMetrics() []TransferMetric {
	d.mu.RLock()
	defer d.mu.RUnlock()
	metrics := make([]TransferMetric, len(d.metrics))
	copy(metrics, d.metrics)
	return metrics
}

func (d *Downloader) IsDone() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.done
}

func (d *Downloader) SetSegmentSize(size int64) {
	d.currentSize = size
}

func (d *Downloader) GetSegmentSize() int64 {
	return d.currentSize
}

func (d *Downloader) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics = make([]TransferMetric, numConcurrent)
	for i := range d.ewmas {
		d.ewmas[i] = ewma.NewMovingAverage()
	}
	d.done = false
}
