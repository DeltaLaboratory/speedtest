package main

import "fmt"

type Cloudflare struct{}

func (c Cloudflare) Name() string {
	return "Cloudflare"
}

func (c Cloudflare) DownloadURL(size int64) string {
	return fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", size)
}

func (c Cloudflare) DownloadLimit() int64 {
	return 100 * 1024 * 1024
}

func (c Cloudflare) UploadURL(_ ...int64) string {
	return "https://speed.cloudflare.com/__up"
}

func (c Cloudflare) UploadLimit() int64 {
	return 100 * 1024 * 1024
}

type Benchbee struct{}

func (b Benchbee) Name() string {
	return "Benchbee"
}

func (b Benchbee) DownloadURL(size int64) string {
	return fmt.Sprintf("http://211.174.55.226:8090/download?size=%d", size)
}

func (b Benchbee) DownloadLimit() int64 {
	return 25 * 1024 * 1024
}

func (b Benchbee) UploadURL(_ ...int64) string {
	return "http://211.174.55.226:8090/upload"
}

func (b Benchbee) UploadLimit() int64 {
	return 100 * 1024 * 1024
}
