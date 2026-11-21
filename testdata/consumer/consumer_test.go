package consumer_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"example.com/consumer"
)

// TestPlanFromOutsideTheModule plans a network built in code and checks the result a real
// consumer would rely on: a valid plan, a config for every node, and every output file.
func TestPlanFromOutsideTheModule(t *testing.T) {
	result, err := consumer.PlanThreeSite()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !result.Validation.OK || len(result.Configs) != 3 {
		t.Fatalf("ok %v, %d configs", result.Validation.OK, len(result.Configs))
	}
	if !strings.HasPrefix(result.PlanID, "consumer-three-site@") {
		t.Errorf("plan ID %q", result.PlanID)
	}
	files, err := result.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, name := range []string{"plan.json", "ipam.json", "routes.txt", "validation.json", "simulation.json", "nodes/builder-1.json"} {
		if len(files[name]) == 0 {
			t.Errorf("%s was not rendered", name)
		}
	}
	var summary struct{ OK bool }
	if err := json.Unmarshal(files["plan.json"], &summary); err != nil || !summary.OK {
		t.Errorf("plan.json: %v (ok %v)", err, summary.OK)
	}
}

// TestPlanIsTheSameTwice checks a consumer gets the same bytes from the same inputs.
func TestPlanIsTheSameTwice(t *testing.T) {
	a, _ := consumer.PlanThreeSite()
	b, _ := consumer.PlanThreeSite()
	fa, _ := a.Render()
	fb, _ := b.Render()
	for name, data := range fa {
		if string(fb[name]) != string(data) {
			t.Errorf("%s differs between two plans of the same input", name)
		}
	}
}

// forbidden lists what a consumer must never pull in by depending on netctl: anything that
// runs programs, talks to the network or drives the operating system. It is the architecture
// guard seen from the outside.
var forbidden = []string{"os/exec", "net/http", "database/sql", "golang.org/x/sys", "github.com/vishvananda/netlink", "golang.zx2c4.com/wireguard"}

// TestTheConsumerPullsInNoForbiddenPackage lists everything the consumer's package imports,
// directly or not, and fails if any of it is a package netctl promises never to use.
func TestTheConsumerPullsInNoForbiddenPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "example.com/consumer").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbidden {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("depending on netctl pulls in %s", dep)
			}
		}
	}
}
