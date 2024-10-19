package main

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type Downloader struct {
	provider    Provider
	currentSize int64
	metrics     []TransferMetric
	done        bool
	mu          sync.RWMutex
}

func NewDownloader(provider Provider) *Downloader {
	return &Downloader{
		provider: provider,
		metrics:  make([]TransferMetric, numConcurrent),
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
	var downloaded int64

	// Initialize a SlidingWindowCounter for tracking speed
	metric := NewSlidingWindowCounter(10*time.Second, 1*time.Second)

	for {
		d.mu.RLock()
		remaining := d.currentSize - downloaded
		d.mu.RUnlock()

		if remaining <= 0 {
			d.updateMetric(id, TransferMetric{Progress: 1, Speed: int64(metric.Value()), Done: true})
			return
		}

		downloadSize := min(downloadLimit, remaining)

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

		// Ensure the response body is closed
		func() {
			defer resp.Body.Close()

			buffer := make([]byte, 16*1024) // 16 KiB buffer
			for {
				select {
				case <-ctx.Done():
					d.updateMetric(id, TransferMetric{Err: ctx.Err(), Done: true})
					return
				default:
					n, err := resp.Body.Read(buffer)
					if n > 0 {
						downloaded += int64(n)
						metric.Add(int64(n))
						progress := float64(downloaded) / float64(d.currentSize)

						d.updateMetric(id, TransferMetric{
							Progress: progress,
							Speed:    int64(metric.Value()),
							Done:     progress >= 1,
						})
					}

					if err != nil {
						if err != io.EOF {
							d.updateMetric(id, TransferMetric{Err: err, Done: true})
						}
						return
					}

					if downloaded >= d.currentSize {
						d.updateMetric(id, TransferMetric{Progress: 1, Speed: int64(metric.Value()), Done: true})
						return
					}
				}
			}
		}()
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
	metricsCopy := make([]TransferMetric, len(d.metrics))
	copy(metricsCopy, d.metrics)
	return metricsCopy
}

func (d *Downloader) IsDone() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.done
}

func (d *Downloader) SetSegmentSize(size int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.currentSize = size
}

func (d *Downloader) GetSegmentSize() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentSize
}

func (d *Downloader) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics = make([]TransferMetric, numConcurrent)
	d.done = false
}
