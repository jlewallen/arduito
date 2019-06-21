package main

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Properties struct {
	Map map[string]string
}

type Boards struct {
	Properties
}

type Platforms struct {
	Properties
}

func (b *Properties) AddUnder(parent string, name string) error {
	files := []string{}
	err := filepath.Walk(parent, func(path string, f os.FileInfo, err error) error {
		files = append(files, path)
		return nil
	})
	if err != nil {
		log.Fatalf("%v", err)
	}

	for _, file := range files {
		if filepath.Base(file) == name {
			log.Printf("Reading %s", file)
			b.Add(file)
		}
	}

	return nil
}

func (b *Properties) Add(path string) error {
	file, err := os.Open(path)
	if err != nil {
		log.Fatalf("%v", err)
	}

	defer file.Close()

	rem := regexp.MustCompile("#.+")

	if b.Map == nil {
		b.Map = make(map[string]string)
	}

	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(rem.ReplaceAllString(s.Text(), ""))
		if len(line) > 0 {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				if b.Map[parts[0]] != "" {
					log.Printf("Key collision %v", parts[0])
				}
				b.Map[parts[0]] = parts[1]
			}
		}
	}

	if err := s.Err(); err != nil {
		log.Fatal(err)
	}

	return nil
}

func (p *Properties) Narrow(prefix string) *Properties {
	n := &Properties{
		Map: make(map[string]string),
	}

	dotted := prefix + "."

	for key, value := range p.Map {
		if strings.HasPrefix(key, dotted) {
			n.Map[key[len(dotted):]] = value
		}
	}

	return n
}
