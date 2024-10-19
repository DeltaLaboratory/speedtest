package main

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

type Uploader struct {
	provider    Provider
	currentSize int64
	metrics     []TransferMetric
	done        bool
	mu          sync.RWMutex
}

// NewUploader creates and initializes a new Uploader instance.
func NewUploader(provider Provider) *Uploader {
	return &Uploader{
		provider: provider,
		metrics:  make([]TransferMetric, numConcurrent),
	}
}

// OperationType returns the type of operation, which is "Upload".
func (u *Uploader) OperationType() string {
	return "Upload"
}

// Provider returns the associated Provider.
func (u *Uploader) Provider() Provider {
	return u.provider
}

// Start begins the upload process by launching concurrent upload workers.
func (u *Uploader) Start() {
	var wg sync.WaitGroup
	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			u.uploadWorker(id)
		}(i)
	}

	// Monitor the completion of all upload workers.
	go func() {
		wg.Wait()
		u.mu.Lock()
		u.done = true
		u.mu.Unlock()
	}()
}

// uploadWorker handles the upload process for a single worker.
func (u *Uploader) uploadWorker(id int) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()

	uploadLimit := u.provider.UploadLimit()
	var uploaded int64

	// Initialize a SlidingWindowCounter for tracking upload speed.
	metric := NewSlidingWindowCounter(10*time.Second, 1*time.Second)

	for {
		// Safely retrieve the remaining bytes to upload.
		u.mu.RLock()
		remaining := u.currentSize - uploaded
		u.mu.RUnlock()

		if remaining <= 0 {
			// Upload completed for this worker.
			u.updateMetric(id, TransferMetric{
				Progress: 1,
				Speed:    int64(metric.Value()),
				Done:     true,
			})
			return
		}

		// Determine the size of the next upload chunk.
		uploadSize := min(uploadLimit, remaining)

		randomReader := io.LimitReader(rand.NewChaCha8([32]byte{}), uploadSize)

		// Wrap the random reader with ProgressReader to monitor upload progress.
		progressReader := &ProgressReader{
			Reader:    randomReader,
			StartTime: time.Now(),
			SpeedUpdate: func(bytesRead int64) {
				metric.Add(bytesRead)
				uploaded += bytesRead
				progress := float64(uploaded) / float64(u.currentSize)

				u.updateMetric(id, TransferMetric{
					Progress: progress,
					Speed:    int64(metric.Value()),
					Done:     progress >= 1,
				})
			},
		}

		// Create a new HTTP POST request with the progress reader as the body.
		req, err := http.NewRequestWithContext(ctx, "POST", u.provider.UploadURL(uploadSize), progressReader)
		if err != nil {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}

		// Set the appropriate headers for the upload.
		req.ContentLength = uploadSize
		req.Header.Set("Content-Type", "application/octet-stream")

		// Execute the HTTP request.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}

		// Ensure the response body is fully read and closed to prevent resource leaks.
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil && err != io.EOF {
			u.updateMetric(id, TransferMetric{Err: err, Done: true})
			return
		}

		// If the context has been canceled, terminate the upload.
		if ctx.Err() != nil {
			u.updateMetric(id, TransferMetric{Err: ctx.Err(), Done: true})
			return
		}

		// Check if the upload has completed.
		if uploaded >= u.currentSize {
			u.updateMetric(id, TransferMetric{
				Progress: 1,
				Speed:    int64(metric.Value()),
				Done:     true,
			})
			return
		}
	}
}

// updateMetric safely updates the metrics for a given worker ID.
func (u *Uploader) updateMetric(id int, metric TransferMetric) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if id >= 0 && id < len(u.metrics) {
		u.metrics[id] = metric
	}
}

// GetMetrics retrieves a copy of the current transfer metrics.
func (u *Uploader) GetMetrics() []TransferMetric {
	u.mu.RLock()
	defer u.mu.RUnlock()
	metricsCopy := make([]TransferMetric, len(u.metrics))
	copy(metricsCopy, u.metrics)
	return metricsCopy
}

// IsDone checks whether all upload operations have completed.
func (u *Uploader) IsDone() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.done
}

// SetSegmentSize sets the total size of the upload segment.
func (u *Uploader) SetSegmentSize(size int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.currentSize = size
}

// GetSegmentSize retrieves the current upload segment size.
func (u *Uploader) GetSegmentSize() int64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.currentSize
}

// Reset clears the current metrics and marks the uploader as not done.
func (u *Uploader) Reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.metrics = make([]TransferMetric, numConcurrent)
	u.done = false
}

// ProgressReader wraps an io.Reader to track the progress of data transfer.
type ProgressReader struct {
	io.Reader
	BytesRead   int64
	StartTime   time.Time
	SpeedUpdate func(int64)
}

// Read reads data into p, updates the bytes read, and invokes the SpeedUpdate callback.
func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	if n > 0 && pr.SpeedUpdate != nil {
		pr.SpeedUpdate(int64(n))
	}
	return n, err
}
