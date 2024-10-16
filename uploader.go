package main

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/VividCortex/ewma"
)

type Uploader struct {
	provider    Provider
	currentSize int64
	metrics     []TransferMetric
	done        bool
	mu          sync.RWMutex
	ewmas       []ewma.MovingAverage
}

func NewUploader(provider Provider) *Uploader {
	ewmas := make([]ewma.MovingAverage, numConcurrent)
	for i := range ewmas {
		ewmas[i] = ewma.NewMovingAverage(5)
	}
	return &Uploader{
		provider: provider,
		metrics:  make([]TransferMetric, numConcurrent),
		ewmas:    ewmas,
	}
}

func (u *Uploader) OperationType() string {
	return "Upload"
}

func (u *Uploader) Provider() Provider {
	return u.provider
}

func (u *Uploader) Start() {
	var wg sync.WaitGroup
	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			u.uploadWorker(id)
		}(i)
	}

	go func() {
		wg.Wait()
		u.mu.Lock()
		u.done = true
		u.mu.Unlock()
	}()
}

func (u *Uploader) uploadWorker(id int) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()

	uploadLimit := u.provider.UploadLimit()

	uploaded := int64(0)
	for u.currentSize > uploaded {
		uploadSize := min(uploadLimit, u.currentSize-uploaded)

		metric := NewSpeedMeter(1 * time.Second)
		randomReader := io.LimitReader(rand.NewChaCha8([32]byte{}), u.currentSize)

		progressReader := &ProgressReader{
			Reader:    randomReader,
			StartTime: time.Now(),
		}

		progressReader.SpeedUpdate = func(bytesRead int64) {
			metric.Add(bytesRead)
			uploaded += bytesRead
			progress := float64(uploaded) / float64(u.currentSize)

			u.ewmas[id].Add(metric.Value())
			smoothedSpeed := u.ewmas[id].Value()

			u.updateMetric(id, TransferMetric{
				Progress: progress,
				Speed:    smoothedSpeed * 8 / 1024 / 1024,
				Done:     progress >= 1,
			})
		}

		req, err := http.NewRequestWithContext(ctx, "POST", u.provider.UploadURL(uploadSize), progressReader)
		if err != nil {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}

		req.ContentLength = u.currentSize
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}
		defer resp.Body.Close()

		_, err = io.Copy(io.Discard, resp.Body)
		if err != nil {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
		}
	}
}

func (u *Uploader) updateMetric(id int, metric TransferMetric) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.metrics[id] = metric
}

func (u *Uploader) GetMetrics() []TransferMetric {
	u.mu.RLock()
	defer u.mu.RUnlock()
	metrics := make([]TransferMetric, len(u.metrics))
	copy(metrics, u.metrics)
	return metrics
}

// IsDone method for Uploader (new code)
func (u *Uploader) IsDone() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.done
}

func (u *Uploader) SetSegmentSize(size int64) {
	u.currentSize = size
}

func (u *Uploader) GetSegmentSize() int64 {
	return u.currentSize
}

func (u *Uploader) Reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.metrics = make([]TransferMetric, numConcurrent)
	for i := range u.ewmas {
		u.ewmas[i] = ewma.NewMovingAverage()
	}
	u.done = false
}

type ProgressReader struct {
	io.Reader
	BytesRead   int64
	StartTime   time.Time
	SpeedUpdate func(int64)
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.BytesRead += int64(n)
	pr.SpeedUpdate(int64(n))

	return n, err
}
