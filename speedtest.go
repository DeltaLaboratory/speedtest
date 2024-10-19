package main

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Configuration parameters
var (
	// Number of concurrent connections, default is 8
	numConcurrent int
	// Initial segment size in bytes, default is 5MB
	initialSize int64
	// Maximum segment size in bytes, default is 128MB
	maxSize int64
	// UI update interval, default is 75ms
	updateInterval time.Duration
	// Operation timeout, default is 30s
	operationTimeout time.Duration
)

// Initialize configuration parameters
func init() {
	flag.IntVar(&numConcurrent, "concurrent", 8, "Number of concurrent connections")
	flag.Int64Var(&initialSize, "initial", 5*1024*1024, "Initial segment size in bytes")
	flag.Int64Var(&maxSize, "max", 128*1024*1024, "Maximum segment size in bytes")
	flag.DurationVar(&updateInterval, "update", 75*time.Millisecond, "UI update interval")
	flag.DurationVar(&operationTimeout, "timeout", 30*time.Second, "Operation timeout")
}

// TransferMetric represents metrics for both download and upload operations
type TransferMetric struct {
	Progress float64
	Speed    int64
	Done     bool
	Err      error
}

// BandwidthOperation interface for both download and upload operations
type BandwidthOperation interface {
	OperationType() string
	Provider() Provider

	Start()
	GetMetrics() []TransferMetric
	IsDone() bool

	SetSegmentSize(size int64)
	GetSegmentSize() int64

	Reset()
}

// Provider interface for speed test providers
type Provider interface {
	Name() string

	DownloadURL(size int64) string
	DownloadLimit() int64
	UploadURL(size ...int64) string
	UploadLimit() int64
}

type tickMsg time.Time

// SizeTestResult stores the result of a single segment size test
type SizeTestResult struct {
	SegmentSize  int64
	TotalSpeed   int64
	AverageSpeed int64
}

// model represents the application state
type model struct {
	bandwidth   BandwidthOperation
	quitting    bool
	progresses  []progress.Model
	segmentSize int64

	testComplete bool

	totalSpeedMetric  *SlidingWindowCounter
	singleSpeedMetric *SlidingWindowCounter

	results []SizeTestResult
}

// initialModel creates and initializes a new model
func initialModel(bandwidth BandwidthOperation) model {
	bandwidth.SetSegmentSize(initialSize)
	bandwidth.Start()
	progresses := make([]progress.Model, numConcurrent)
	for i := range progresses {
		progresses[i] = progress.New(progress.WithDefaultGradient())
	}
	return model{
		bandwidth:   bandwidth,
		progresses:  progresses,
		segmentSize: initialSize,

		totalSpeedMetric:  NewSlidingWindowCounter(10*time.Second, 1*time.Second),
		singleSpeedMetric: NewSlidingWindowCounter(10*time.Second, 1*time.Second),

		testComplete: false,
		results:      []SizeTestResult{},
	}
}

// Init initializes the model
func (m model) Init() tea.Cmd {
	return tick()
}

// Update handles updates to the model
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			return m, tea.Quit
		}
	case tickMsg:
		if m.bandwidth.IsDone() {
			if !m.testComplete {
				m.testComplete = true
				// Save result for current segment size
				m.results = append(m.results, SizeTestResult{
					SegmentSize:  m.segmentSize,
					TotalSpeed:   int64(m.totalSpeedMetric.Value() / 10),
					AverageSpeed: int64(m.singleSpeedMetric.Value() / 10),
				})
				nextSize := calculateNextSize(m.segmentSize, m.totalSpeedMetric.Value())
				if nextSize > m.segmentSize && nextSize <= maxSize {
					m.totalSpeedMetric.Reset()
					m.singleSpeedMetric.Reset()

					m.segmentSize = nextSize
					m.bandwidth.SetSegmentSize(m.segmentSize)
					m.bandwidth.Reset()
					m.bandwidth.Start()
					m.testComplete = false
					for i := range m.progresses {
						m.progresses[i].SetPercent(0)
					}
				} else {
					m.quitting = true
					return m, tea.Quit
				}
			}
		}

		metrics := m.bandwidth.GetMetrics()
		var totalSpeed int64
		for _, metric := range metrics {
			totalSpeed += metric.Speed
		}

		m.totalSpeedMetric.Add(totalSpeed)
		m.singleSpeedMetric.Add(totalSpeed / int64(len(metrics)))

		for i, metric := range metrics {
			m.progresses[i].SetPercent(metric.Progress)
		}

		return m, tick()
	}
	return m, nil
}

// View renders the current state of the model
func (m model) View() string {
	if m.quitting {
		return m.finalView()
	}

	metrics := m.bandwidth.GetMetrics()
	operationType := m.bandwidth.OperationType()

	s := fmt.Sprintf("Concurrent %s Speed Test | %s\n\n", operationType, m.bandwidth.Provider().Name())
	s += fmt.Sprintf("Current segment size: %s\n\n", formatSize(m.segmentSize))

	for i, metric := range metrics {
		if metric.Err != nil {
			s += fmt.Sprintf("%s %d: Error - %v\n\n", operationType, i+1, metric.Err)
		} else {
			s += fmt.Sprintf("%s %d: %.1f%% | Speed: %s\t%s\n",
				operationType, i+1, metric.Progress*100,
				formatSpeed(metric.Speed),
				m.progresses[i].ViewAs(metric.Progress))
		}
	}

	s += fmt.Sprintf("\nTotal Connection Speed: %s\n", formatSpeed(int64(m.totalSpeedMetric.Value()/10)))
	s += fmt.Sprintf("Single Connection Speed: %s\n", formatSpeed(int64(m.singleSpeedMetric.Value()/10)))
	s += "\nPress Ctrl+C to quit\n"
	return s
}

// finalView renders the final results view
func (m model) finalView() string {
	operationType := m.bandwidth.OperationType()
	s := fmt.Sprintf("%s Test Results:\n\n", operationType)

	for _, result := range m.results {
		s += fmt.Sprintf("Segment Size: %s\n", formatSize(result.SegmentSize))
		s += fmt.Sprintf("Total Connection Speed: %s\n", formatSpeed(result.TotalSpeed))
		s += fmt.Sprintf("Single Connection Speed: %s\n", formatSpeed(result.AverageSpeed))
		s += strings.Repeat("-", 40) + "\n"
	}

	return s
}

func tick() tea.Cmd {
	return tea.Tick(updateInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type providerItem struct {
	name        string
	description string
}

func (i providerItem) Title() string       { return i.name }
func (i providerItem) Description() string { return i.description }
func (i providerItem) FilterValue() string { return i.name }

type providerSelectionModel struct {
	list     list.Model
	choice   Provider
	quitting bool
}

func initialProviderSelectionModel() providerSelectionModel {
	items := []list.Item{
		providerItem{name: "Cloudflare", description: "Cloudflare Speed test (https://speed.cloudflare.com)"},
		providerItem{name: "Benchbee", description: "Benchbee Speed test (https://speed.benchbee.co.kr)"},
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Select a provider"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = lipgloss.NewStyle().MarginLeft(2).MarginTop(1).Bold(true)

	return providerSelectionModel{list: l}
}

func (m providerSelectionModel) Init() tea.Cmd {
	return nil
}

func (m providerSelectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height - 4) // Subtract 4 to leave room for the title and margins
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			i, ok := m.list.SelectedItem().(providerItem)
			if ok {
				switch i.name {
				case "Cloudflare":
					m.choice = Cloudflare{}
				case "Benchbee":
					m.choice = Benchbee{}
				default:
					fmt.Printf("Error: Unknown provider %s\n", i.name)
					return m, tea.Quit
				}
			}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m providerSelectionModel) View() string {
	return "\n" + m.list.View()
}

func main() {
	flag.Parse()

	providerSelectionP := tea.NewProgram(initialProviderSelectionModel())
	providerModel, err := providerSelectionP.Run()
	if err != nil {
		fmt.Printf("Error running provider selection: %v\n", err)
		return
	}

	selectedProvider := providerModel.(providerSelectionModel).choice
	if selectedProvider == nil {
		fmt.Println("No provider selected. Exiting.")
		return
	}

	// Run download benchmark
	fmt.Println("Running Download Benchmark...")
	p := tea.NewProgram(initialModel(NewDownloader(selectedProvider)))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running download benchmark: %v\n", err)
	}

	// Run upload benchmark
	fmt.Println("\nRunning Upload Benchmark...")
	p = tea.NewProgram(initialModel(NewUploader(selectedProvider)))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running upload benchmark: %v\n", err)
	}

	// Wait for user input before exiting
	fmt.Println("\nPress Enter to exit")
	_, _ = fmt.Scanln()
}
