package ptimport

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"
)

// Route is one line of show ip route, as transcribed.
type Route struct {
	// Code is the Cisco route code: C connected, S static.
	Code string `yaml:"code"`
	// Prefix is the destination network.
	Prefix netip.Prefix `yaml:"prefix"`
	// Via is the next-hop address, or the word "connected".
	Via string `yaml:"via"`
	// Backup marks a floating static route: one that is in the configuration but not in the
	// routing table while the primary is up.
	Backup bool `yaml:"backup"`
}

// Traceroute is one traceroute, as transcribed.
type Traceroute struct {
	// From is the host or router the trace started on.
	From string `yaml:"from"`
	// To is the destination address.
	To string `yaml:"to"`
	// Hops are the addresses that answered, in order, the destination last.
	Hops []string `yaml:"hops"`
	// State names the lab state the trace was taken in ("baseline", "shutdown", "restored"),
	// for labs that change; empty means the only state.
	State string `yaml:"state,omitempty"`
}

// Device is one file of an observation: one device and what it showed.
type Device struct {
	// Device is the name in Packet Tracer, for example "R-HUB".
	Device string `yaml:"device"`
	// MapsTo is the node ID it stands for, for example "aws-hub".
	MapsTo string `yaml:"maps_to"`
	// ShowIPRoute is the routing table, one entry per line.
	ShowIPRoute []Route `yaml:"show_ip_route"`
	// Traceroutes are the traces taken from this device.
	Traceroutes []Traceroute `yaml:"traceroutes"`
	// State names the lab state the table was taken in; empty means the only state.
	State string `yaml:"state,omitempty"`
}

// Mapping is the optional mapping.yaml of an observation: names and addresses that the
// plan's allocations cannot tell the importer.
type Mapping struct {
	// Hosts maps a host name (a PC) to the node whose site it sits in.
	Hosts map[string]string `yaml:"hosts"`
	// Hops maps an address to the node that owns it, for addresses the plan does not
	// allocate (a hub link's /30, which has no transit allocation).
	Hops map[string]string `yaml:"hops"`
}

// Observation is everything transcribed for one lab.
type Observation struct {
	// Devices are the device files, sorted by name.
	Devices []Device
	// Mapping is mapping.yaml, or the zero value.
	Mapping Mapping
}

// Parse reads an observation from the contents of its files, by file name. A file named
// mapping.yaml is the Mapping; every other .yaml file is a Device. Unknown fields are
// errors, because a typo in a transcription must not pass for an empty table.
//
// Example:
//
//	obs, err := ptimport.Parse(map[string][]byte{"R-HUB.yaml": data})
func Parse(files map[string][]byte) (Observation, error) {
	var obs Observation
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		dec := yaml.NewDecoder(bytesReader(files[name]))
		dec.KnownFields(true)
		if name == "mapping.yaml" {
			if err := dec.Decode(&obs.Mapping); err != nil {
				return obs, fmt.Errorf("%s: %w", name, err)
			}
			continue
		}
		var d Device
		if err := dec.Decode(&d); err != nil {
			return obs, fmt.Errorf("%s: %w", name, err)
		}
		if d.Device == "" || d.MapsTo == "" {
			return obs, fmt.Errorf("%s: device and maps_to are required", name)
		}
		obs.Devices = append(obs.Devices, d)
	}
	return obs, nil
}

// Load reads every .yaml file of a directory into an Observation. A missing directory is
// reported as os.ErrNotExist so a test can skip a lab whose results are not transcribed yet.
func Load(dir string) (Observation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Observation{}, err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return Observation{}, err
		}
		files[e.Name()] = data
	}
	if len(files) == 0 {
		return Observation{}, fmt.Errorf("%s: no observation files: %w", dir, os.ErrNotExist)
	}
	return Parse(files)
}

// States returns the lab states the observation covers, sorted; "" stands for the only state.
func (o Observation) States() []string {
	seen := map[string]bool{}
	for _, d := range o.Devices {
		seen[d.State] = true
		for _, t := range d.Traceroutes {
			seen[t.State] = true
		}
	}
	states := make([]string, 0, len(seen))
	for s := range seen {
		states = append(states, s)
	}
	slices.Sort(states)
	return states
}
