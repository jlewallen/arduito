package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/codeclysm/extract"
	version "github.com/hashicorp/go-version"
	"github.com/mholt/archiver"
	"gopkg.in/cheggaaa/pb.v1"
)

type PlatformBoard struct {
	Name string `json:"name"`
}

type ToolDependency struct {
	Packager string `json:"packager"`
	Version  string `json:"version"`
	Name     string `json:"name"`
}

type PackagePlatform struct {
	Name              string           `json:"name"`
	Architecture      string           `json:"architecture"`
	Version           string           `json:"version"`
	Category          string           `json:"category"`
	Help              Help             `json:"help"`
	URL               string           `json:"url"`
	ArchiveFileName   string           `json:"archiveFileName"`
	Checksum          string           `json:"checksum"`
	Size              string           `json:"size"`
	Boards            []PlatformBoard  `json:"boards"`
	ToolsDependencies []ToolDependency `json:"toolsDependencies"`
}

type Help struct {
	Online string `json:"online"`
}

type Package struct {
	Name       string            `json:"name"`
	Maintainer string            `json:"maintainer"`
	WebsiteURL string            `json:"websiteURL"`
	Email      string            `json:"email"`
	Platforms  []PackagePlatform `json:"platforms"`
	Tools      []Tool            `json:"tools"`
}

type ToolSystem struct {
	URL             string `json:"url"`
	Checksum        string `json:"checksum"`
	Host            string `json:"host"`
	ArchiveFileName string `json:"archiveFileName"`
	Size            string `json:"size"`
}

type Tool struct {
	Version string       `json:"version"`
	Name    string       `json:"name"`
	Systems []ToolSystem `json:"systems"`
}

type PackagesIndex struct {
	Packages []Package `json:"packages"`
}

type PackagesCollection struct {
	Indices []*PackagesIndex
}

type ByVersion []*LatestPackage

func (a ByVersion) Len() int      { return len(a) }
func (a ByVersion) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByVersion) Less(i, j int) bool {
	vA, _ := version.NewVersion(a[i].Platform.Version)
	vB, _ := version.NewVersion(a[j].Platform.Version)
	return vB.LessThan(vA)
}

func (pc *PackagesCollection) Add(name string) {
	if pc.Indices == nil {
		pc.Indices = make([]*PackagesIndex, 0)
	}
	data, err := ioutil.ReadFile(name)
	if err != nil {
		log.Fatalf("%v", err)
	}

	packages := &PackagesIndex{}
	err = json.Unmarshal(data, packages)
	if err != nil {
		log.Fatalf("%v", err)
	}

	pc.Indices = append(pc.Indices, packages)
}

func (pc *PackagesCollection) FindTools(td ToolDependency) *Tool {
	for _, index := range pc.Indices {
		for _, pkg := range index.Packages {
			for _, t := range pkg.Tools {
				if t.Name == td.Name && t.Version == td.Version {
					return &t
				}
			}
		}
	}

	return nil
}

func (tool *Tool) ForSystem(allowed []string) *ToolSystem {
	for _, ts := range tool.Systems {
		for _, candidate := range allowed {
			if candidate == ts.Host {
				return &ts
			}
		}
	}
	return nil
}

type LatestPackage struct {
	Package  Package
	Platform PackagePlatform
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func (pc *PackagesCollection) Architecture(packages []string, arch string, specificVersion string) map[string]*LatestPackage {
	byName := map[string][]*LatestPackage{}
	latest := map[string]*LatestPackage{}
	for _, index := range pc.Indices {
		for _, pkg := range index.Packages {
			if !contains(packages, pkg.Name) {
				continue
			}
			for _, p := range pkg.Platforms {
				if p.Architecture == arch {
					if byName[p.Name] == nil {
						byName[p.Name] = make([]*LatestPackage, 0)
					}
					byName[p.Name] = append(byName[p.Name], &LatestPackage{
						Package:  pkg,
						Platform: p,
					})
				}
			}
		}
	}

	for name, all := range byName {
		sort.Sort(ByVersion(all))
		latest[name] = all[0]

		if specificVersion != "" {
			for _, lp := range all {
				if specificVersion == lp.Platform.Version {
					latest[name] = lp
				}
			}
		}
	}

	return latest
}

type InstallationPlan struct {
	Files []InstallFile
}

type InstallFile struct {
	Path     string
	FileName string
	URL      string
	Size     uint32
}

func (ip *InstallationPlan) Add(path, downloadUrl, size string) {
	u, err := url.Parse(downloadUrl)
	if err != nil {
		log.Fatalf("%v", err)
	}
	sz, err := strconv.ParseInt(size, 10, 32)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fileName := filepath.Base(u.EscapedPath())
	ip.Files = append(ip.Files, InstallFile{
		Path:     path,
		FileName: fileName,
		URL:      downloadUrl,
		Size:     uint32(sz),
	})
}

func (ip *InstallationPlan) Execute() {
	downloading := make([]InstallFile, 0)
	for _, file := range ip.Files {
		if _, err := os.Stat(file.FileName); os.IsNotExist(err) {
			downloading = append(downloading, file)
		}
	}

	bars := make([]*pb.ProgressBar, 0)
	for _, file := range downloading {
		bars = append(bars, pb.New(int(file.Size)))
	}

	pool, err := pb.StartPool(bars...)
	if err != nil {
		log.Fatalf("%v", err)
	}
	wg := new(sync.WaitGroup)

	for i, file := range downloading {
		wg.Add(1)
		go func(file InstallFile, bar *pb.ProgressBar) {
			downloadFile(file, bar)
			wg.Done()
		}(file, bars[i])
	}

	wg.Wait()

	pool.Stop()

	tempDir, err := ioutil.TempDir("", "arduino-packages")
	if err != nil {
		log.Fatalf("%v", err)
	}

	defer os.RemoveAll(tempDir)

	for _, file := range ip.Files {
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			log.Printf("Extracting: %v to %v", file.FileName, file.Path)

			err := os.MkdirAll(path.Dir(file.Path), 0755)
			if err != nil {
				log.Fatalf("%v", err)
			}

			if false {
				err = archiver.Unarchive(file.FileName, tempDir)
				if err != nil {
					log.Fatalf("Unable to unarchive %v: %v", file.FileName, err)
				}
			} else {
				file, err := os.Open(file.FileName)
				if err != nil {
					log.Fatal(err)
				}
				extract.Archive(context.Background(), file, tempDir, nil)
			}

			extracted, err := ioutil.ReadDir(tempDir)
			if err != nil {
				log.Fatalf("%v", err)
			}

			if len(extracted) != 1 {
				log.Fatalf("Extracted more than one directory, I'm confused")
			}

			err = os.Rename(path.Join(tempDir, extracted[0].Name()), file.Path)
			if err != nil {
				log.Fatalf("%v", err)
			}
		}
	}
}

func downloadFile(file InstallFile, bar *pb.ProgressBar) {
	client := &http.Client{}
	response, err := client.Get(file.URL)
	if err != nil {
		log.Fatalf("%v", err)
	}

	defer response.Body.Close()
	reader := bar.NewProxyReader(response.Body)

	out, err := os.Create(file.FileName)
	if err != nil {
		log.Fatalf("%v", err)
	}

	defer out.Close()

	_, err = io.Copy(out, reader)
	if err != nil {
		log.Fatalf("%v", err)
	}
}

type options struct {
	RootDirectory string
}

func main() {
	o := options{}

	flag.StringVar(&o.RootDirectory, "root-directory", "/tmp/working", "directory to extract to")

	flag.Parse()

	pc := &PackagesCollection{}
	pc.Add("package_adafruit_index.json")
	pc.Add("package_index.json")

	adafruit := pc.Architecture([]string{"adafruit"}, "samd", "1.2.9")
	arduino := pc.Architecture([]string{"arduino"}, "samd", "1.6.17")

	packages := []*LatestPackage{}
	for _, p := range arduino {
		packages = append(packages, p)
	}
	for _, p := range adafruit {
		packages = append(packages, p)
	}

	plan := &InstallationPlan{
		Files: make([]InstallFile, 0),
	}

	rootDirectory := o.RootDirectory
	boardPaths := make([]string, 0)
	// systems := []string{"x86_64-apple-darwin", "i386-apple-darwin11"}
	systems := []string{"x86_64-pc-linux-gnu", "x86_64-linux-gnu"}

	for _, p := range packages {
		hwPath := fmt.Sprintf("%s/%s/%s/%s/%s", rootDirectory, p.Package.Name, "hardware", p.Platform.Architecture, p.Platform.Version)
		log.Printf("Hardware: %s", hwPath)

		plan.Add(hwPath, p.Platform.URL, p.Platform.Size)

		boardPaths = append(boardPaths, hwPath)

		for _, td := range p.Platform.ToolsDependencies {
			tool := pc.FindTools(td)
			forSystem := tool.ForSystem(systems)
			if forSystem == nil {
				log.Printf("%+v", tool)
				log.Fatalf("Unable to find %v", td)
				continue
			}

			toolPath := fmt.Sprintf("%s/%s/%s/%s/%s", rootDirectory, p.Package.Name, "tools", tool.Name, tool.Version)
			log.Printf("Tool: %s", toolPath)

			plan.Add(toolPath, forSystem.URL, forSystem.Size)
		}
	}

	plan.Execute()

	platforms := &Platforms{}
	boards := &Boards{}
	for _, path := range boardPaths {
		boards.AddUnder(path, "boards.txt")
		platforms.AddUnder(path, "platform.txt")
	}

	boardProperties := boards.Properties.Narrow("adafruit_feather_m0")

	log.Printf("%v", boardProperties)
}
