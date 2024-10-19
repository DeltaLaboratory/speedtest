package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type SlidingWindowCounter struct {
	windowSize    time.Duration // Total window size
	intervalSize  time.Duration // Size of each interval
	intervals     int           // Number of intervals in the window
	currentCount  int64         // Count for the current interval
	previousCount int64         // Count for the previous interval
	lastTick      time.Time     // Last time the interval was updated
	mutex         sync.Mutex    // Mutex for thread-safety
}

// NewSlidingWindowCounter initializes a new sliding window counter
// windowSize: total duration of the sliding window (e.g., 60 seconds)
// intervalSize: duration of each interval (e.g., 1 second)
func NewSlidingWindowCounter(windowSize, intervalSize time.Duration) *SlidingWindowCounter {
	if windowSize <= 0 {
		panic("windowSize must be greater than 0")
	}
	if intervalSize <= 0 {
		panic("intervalSize must be greater than 0")
	}
	if windowSize%intervalSize != 0 {
		panic("windowSize must be a multiple of intervalSize")
	}

	return &SlidingWindowCounter{
		windowSize:    windowSize,
		intervalSize:  intervalSize,
		intervals:     int(windowSize / intervalSize),
		currentCount:  0,
		previousCount: 0,
		lastTick:      time.Now(),
	}
}

func (swc *SlidingWindowCounter) tick() {
	now := time.Now()
	elapsed := now.Sub(swc.lastTick)

	if elapsed < swc.intervalSize {
		return
	}

	// Calculate how many intervals have passed
	intervalsPassed := int(elapsed / swc.intervalSize)
	if intervalsPassed > swc.intervals {
		intervalsPassed = swc.intervals
	}

	// Shift counts for each passed interval
	for i := 0; i < intervalsPassed; i++ {
		swc.previousCount = swc.currentCount
		swc.currentCount = 0
	}

	// Update lastTick
	swc.lastTick = swc.lastTick.Add(time.Duration(intervalsPassed) * swc.intervalSize)
}

func (swc *SlidingWindowCounter) Add(n int64) {
	swc.mutex.Lock()
	defer swc.mutex.Unlock()

	swc.tick()
	swc.currentCount += n
}

func (swc *SlidingWindowCounter) Value() float64 {
	swc.mutex.Lock()
	defer swc.mutex.Unlock()

	swc.tick()

	now := time.Now()
	elapsed := now.Sub(swc.lastTick)
	fraction := float64(elapsed) / float64(swc.intervalSize)
	if fraction > 1.0 {
		fraction = 1.0
	}

	// Interpolate between previousCount and currentCount
	interpolatedCount := float64(swc.previousCount)*(1.0-fraction) + float64(swc.currentCount)*fraction
	return interpolatedCount
}

func (swc *SlidingWindowCounter) Reset() {
	swc.mutex.Lock()
	defer swc.mutex.Unlock()

	swc.currentCount = 0
	swc.previousCount = 0
	swc.lastTick = time.Now()
}

func formatSpeed(speed int64, mode ...int) string {
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	divisor := float64(1000)
	if len(mode) > 0 && mode[0] == 1 {
		units = []string{"B/s", "KB/s", "MB/s", "GB/s", "TB/s"}
		divisor = 1024
	} else {
		speed *= 8 // Convert bytes to bits only if we're not in byte mode
	}
	index := 0
	speedFloat := float64(speed)

	for speedFloat >= divisor && index < len(units)-1 {
		speedFloat /= divisor
		index++
	}

	return fmt.Sprintf("%.2f %s", speedFloat, units[index])
}

func formatSize(sizeBytes int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(sizeBytes)
	index := 0

	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}

	return fmt.Sprintf("%.2f %s", size, units[index])
}

// calculateNextSize determines the optimal segment size for the next test
func calculateNextSize(currentSize int64, speed float64) int64 {
	// Convert speed to bytes per second
	speedBps := speed * 1000 * 1000 / 8

	// Target test duration (in seconds)
	targetDuration := 10.0

	// Calculate ideal size based on current speed and target duration
	idealSize := int64(speedBps * targetDuration)

	// Limit growth rate
	maxGrowth := 2.0
	nextSize := int64(math.Min(float64(currentSize)*maxGrowth, float64(idealSize)))

	// Ensure size is within bounds
	nextSize = int64(math.Max(float64(initialSize), math.Min(float64(nextSize), float64(maxSize))))

	// Round to nearest power of 2 for efficiency
	nextSize = int64(math.Pow(2, math.Round(math.Log2(float64(nextSize)))))

	return nextSize
}

func estimateTotalTestTime(initialSize int64, maxSize int64, initialSpeed float64) time.Duration {
	var totalTime float64
	currentSize := initialSize
	currentSpeed := initialSpeed

	for currentSize <= maxSize {
		testDuration := float64(currentSize) / (currentSpeed * 1000 * 1000 / 8)
		totalTime += testDuration
		currentSize = calculateNextSize(currentSize, currentSpeed)
		// Assume speed increases by 10% each iteration (conservative estimate)
		currentSpeed *= 1.1
	}

	return time.Duration(totalTime * float64(time.Second))
}
