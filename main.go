package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type UrlSet struct {
	URLs []Url `xml:"url"`
}

type Url struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

type Result struct {
	URL           string
	Success       bool
	Attempts      int
	StatusCode    int
	ContentLength string
	Duration      time.Duration
	Error         error
}

func main() {
	var batchSize int
	flag.IntVar(&batchSize, "batch", 1, "Number of concurrent workers (max 20)")
	flag.Parse()

	if batchSize < 1 {
		batchSize = 1
	}
	if batchSize > 20 {
		batchSize = 20
	}

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Usage: go run main.go [--batch N] <sitemap_url>")
		os.Exit(1)
	}

	sitemapURL := args[0]

	resp, err := http.Get(sitemapURL)
	if err != nil {
		fmt.Printf("Error fetching sitemap: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error fetching sitemap: Status code %d\n", resp.StatusCode)
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading sitemap: %v\n", err)
		os.Exit(1)
	}

	var urlSet UrlSet
	err = xml.Unmarshal(body, &urlSet)
	if err != nil {
		fmt.Printf("Error parsing sitemap XML: %v\n", err)
		os.Exit(1)
	}

	totalSites := len(urlSet.URLs)
	fmt.Printf("Processing %d URLs with %d workers...\n", totalSites, batchSize)

	jobs := make(chan string)
	results := make(chan Result)
	var wg sync.WaitGroup

	// Start worker goroutines
	for w := 1; w <= batchSize; w++ {
		wg.Add(1)
		go worker(w, jobs, results, &wg)
	}

	// Send URLs to jobs channel
	go func() {
		for _, url := range urlSet.URLs {
			jobs <- url.Loc
		}
		close(jobs)
	}()

	// Close results channel after all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	resultsList := make([]Result, 0, totalSites)
	for result := range results {
		resultsList = append(resultsList, result)
	}

	// Process results
	total200 := 0
	totalNon200 := 0
	var totalTime time.Duration

	for _, result := range resultsList {
		totalTime += result.Duration
		if result.Success {
			total200++
		} else {
			totalNon200++
		}
	}

	avgTime := time.Duration(0)
	if totalSites > 0 {
		avgTime = totalTime / time.Duration(totalSites)
	}

	fmt.Println("\nSummary:")
	fmt.Printf("Total sites: %d\n", totalSites)
	fmt.Printf("Total 200 responses: %d\n", total200)
	fmt.Printf("Total non-200 responses: %d\n", totalNon200)
	fmt.Printf("Average request time: %v\n", avgTime)
}

func worker(id int, jobs <-chan string, results chan<- Result, wg *sync.WaitGroup) {
	defer wg.Done()
	for url := range jobs {
		result := processURL(url)
		results <- result
	}
}

func processURL(url string) Result {
	var result Result
	result.URL = url
	attempts := 0
	totalDuration := time.Duration(0)

	for attempts < 3 {
		attempts++
		start := time.Now()
		resp, err := http.Get(url)
		duration := time.Since(start)
		totalDuration += duration

		if err != nil {
			// Error occurred
			result.Error = err
			result.StatusCode = 0 // Indicate no status code
			result.Duration = totalDuration
			result.Attempts = attempts
			fmt.Printf("\033[31mAttempt %d: Error visiting %s: %v\033[0m\n", attempts, url, err)
		} else {
			// Ensure the body is fully read and closed
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				// Success
				result.Success = true
				result.StatusCode = resp.StatusCode
				result.ContentLength = resp.Header.Get("Content-Length")
				result.Duration = totalDuration
				result.Attempts = attempts

				fmt.Printf("Attempt %d: Visited %s - Status: %d, Content-Length: %s, Time: %v\n", attempts, url, resp.StatusCode, result.ContentLength, duration)
				return result
			} else {
				// Non-200 status
				result.StatusCode = resp.StatusCode
				result.Duration = totalDuration
				result.Attempts = attempts

				fmt.Printf("\033[31mAttempt %d: Visited %s - Status: %d, Time: %v\033[0m\n", attempts, url, resp.StatusCode, duration)
			}
		}

		if attempts < 3 {
			time.Sleep(1000 * time.Millisecond)
		}
	}

	// Failed after 3 attempts
	fmt.Printf("\033[31mFailed to get 200 status for %s after %d attempts\033[0m\n", url, attempts)
	result.Success = false
	return result
}
