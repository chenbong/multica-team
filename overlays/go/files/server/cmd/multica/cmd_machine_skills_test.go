package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const machineSkillA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const machineSkillB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const machineSkillOther = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

func TestMachineSkillListResolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested []string
		catalog   []machineSkill
		want      []string
		err       string
	}{
		{"names IDs and deduplication", []string{" search ", strings.ToUpper(machineSkillB), machineSkillA}, []machineSkill{{ID: machineSkillA, Name: "search"}, {ID: machineSkillB, Name: "report"}}, []string{machineSkillA, machineSkillB}, ""},
		{"unknown", []string{"missing"}, nil, nil, "not found or is not accessible"},
		{"case sensitive", []string{"Search"}, []machineSkill{{ID: machineSkillA, Name: "search"}}, nil, "not found or is not accessible"},
		{"ambiguous", []string{"search"}, []machineSkill{{ID: machineSkillA, Name: "search"}, {ID: machineSkillB, Name: "search"}}, nil, "ambiguous"},
		{"ID disambiguates", []string{machineSkillB}, []machineSkill{{ID: machineSkillA, Name: "search"}, {ID: machineSkillB, Name: "search"}}, []string{machineSkillB}, ""},
		{"empty", []string{}, nil, nil, "at least one"},
		{"empty item", []string{"search", " "}, nil, nil, "empty or invalid"},
		{"control character", []string{"a\nb"}, nil, nil, "empty or invalid"},
		{"bad catalog ID", []string{"search"}, []machineSkill{{ID: "bad", Name: "search"}}, nil, "invalid identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMachineFixture(t)
			f.skills = tc.catalog
			got, err := machineResolveSkills(t.Context(), f.client, tc.requested)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %v %v", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v %v, want %v", got, err, tc.want)
			}
			if machineHasMutation(f.calls) {
				t.Fatal("resolution mutated state")
			}
		})
	}
}

func TestMachineSkillListFailureBeforeAnyAgentOrMachineMutation(t *testing.T) {
	for _, failure := range []string{"missing", "catalog"} {
		t.Run(failure, func(t *testing.T) {
			f := newMachineFixture(t)
			f.existing()
			f.failSkills = failure
			options := machineDefaults()
			options.MachineName = "Changed machine"
			options.SkillList = []string{"missing"}
			if _, err := enrollMachine(t.Context(), f.client, machineDaemon, options); err == nil {
				t.Fatal("missing skill accepted")
			}
			if machineHasMutation(f.calls) {
				t.Fatalf("invalid list mutated state: %v", f.calls)
			}
		})
	}
}

func TestMachineSkillListCreatesAgentWithAtomicSkillPayload(t *testing.T) {
	f := newMachineFixture(t)
	f.skills = []machineSkill{{ID: machineSkillA, Name: "search"}}
	options := machineDefaults()
	options.SkillList = []string{"search", machineSkillA}
	result, err := enrollMachine(t.Context(), f.client, machineDaemon, options)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(f.payloads[0]["skill_ids"])
	if string(data) != `["`+machineSkillA+`"]` || result["agent_created"] != true {
		t.Fatalf("missing atomic skill payload: %v %v", f.payloads, result)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "/skills/add") {
		t.Fatal("new agent skills were attached in a separate request")
	}
}

func TestMachineSkillListReusesWithoutCancellingAndPreservesOtherAssignments(t *testing.T) {
	f := newMachineFixture(t)
	f.existing()
	f.current["runtime_id"] = machineRT
	f.skills = []machineSkill{{ID: machineSkillA, Name: "search"}, {ID: machineSkillB, Name: "report"}}
	f.agentSkills = []machineSkill{{ID: machineSkillA, Enabled: false}, {ID: machineSkillOther, Enabled: false}}
	options := machineDefaults()
	options.SkillList = []string{"search", "report"}
	for i := 0; i < 2; i++ {
		result, err := enrollMachine(t.Context(), f.client, machineDaemon, options)
		if err != nil || result["agent_id"] != machineAgent || result["agent_created"] != false {
			t.Fatalf("reuse: %v %v", result, err)
		}
	}
	want := []machineSkill{{ID: machineSkillA, Enabled: true}, {ID: machineSkillOther, Enabled: false}, {ID: machineSkillB, Enabled: true}}
	if !reflect.DeepEqual(f.agentSkills, want) {
		t.Fatalf("assignments: %v", f.agentSkills)
	}
	joined := strings.Join(f.calls, "\n")
	if strings.Contains(joined, "/cancel-tasks") || strings.Count(joined, "POST /api/agents/"+machineAgent+"/skills/add") != 1 || strings.Count(joined, "/enabled") != 1 {
		t.Fatalf("non-idempotent update: %s", joined)
	}
	if f.current["instructions"] != "keep instructions" {
		t.Fatal("other settings changed")
	}
}

func TestMachineSkillListBindingFailureIsReportedWithAgentIdentity(t *testing.T) {
	for _, operation := range []string{"add", "enable"} {
		t.Run(operation, func(t *testing.T) {
			f := newMachineFixture(t)
			f.existing()
			f.current["runtime_id"] = machineRT
			f.skills = []machineSkill{{ID: machineSkillA, Name: "search"}}
			if operation == "enable" {
				f.agentSkills = []machineSkill{{ID: machineSkillA, Enabled: false}}
			}
			f.failSkills = operation
			options := machineDefaults()
			options.SkillList = []string{"search"}
			result, err := enrollMachine(context.Background(), f.client, machineDaemon, options)
			if err == nil || result["agent_id"] != machineAgent || result["skill_ids"] != nil {
				t.Fatalf("false success: %v %v", result, err)
			}
		})
	}
}

func TestMachineSkillListFlagOnlyOnAgent(t *testing.T) {
	root := newMachineCommand()
	agent, _, _ := root.Find([]string{"agent"})
	rename, _, _ := root.Find([]string{"rename"})
	if agent.Flags().Lookup("skill-list") == nil || rename.Flags().Lookup("skill-list") != nil {
		t.Fatal("wrong flag scope")
	}
	if err := agent.ParseFlags([]string{"--skill-list", "search,report", "--skill-list", machineSkillA}); err != nil {
		t.Fatal(err)
	}
	values, _ := agent.Flags().GetStringSlice("skill-list")
	if !reflect.DeepEqual(values, []string{"search", "report", machineSkillA}) {
		t.Fatalf("CSV/repeated flag parse: %v", values)
	}
}
