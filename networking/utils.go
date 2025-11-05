package main

import (
	"bytes"
	"bufio"
	"os"
	"path/filepath"
	"time"
	"net/http"
	"sync"
)

func NormPath(path string) (string, error) {
	t, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return t, nil
}

func GetTier1(filename string) ([]string, error) {
	t, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	
	content, err := os.ReadFile(t)
	if err != nil {
		return nil, err
	}

	domains := make([]string, 0)

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		text := scanner.Text()
		domains = append(domains, text)
	}
	
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return domains, nil
}

func FindFastest(domains []string) (string, error) {
	responsetimes := make(map[string]float64)

	var wg sync.WaitGroup
	
	for _, domain := range domains {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			responsetimes[d] = ping(d)
		}(domain)
	}
	wg.Wait()

	for domain, responsetime := range responsetimes {
		if responsetime > 5 {
			delete(responsetimes, domain)
		}
	}

	mindomain := ""
	mintime := 9999999999999999.0
	for domain, responsetime := range responsetimes {
		if responsetime < mintime {
			mindomain = domain
			mintime = responsetime
		}
	}
	return mindomain, nil
}

func ping(domain string) float64 {
	start := time.Now()

	_, err := http.Get("http://" + domain)
	if err != nil {
		return 9999999999999999
	}
	
	return time.Since(start).Seconds()
}