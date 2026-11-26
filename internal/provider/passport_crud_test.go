package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TAIPANBOX/agent-stack-go/v2/passport"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// passportModel is a complete, valid taipan_agent_passport model: every
// attribute typed the way the framework would decode it, output_path set to
// out (or null when out is "").
func passportModel(t *testing.T, out string) *passportResourceModel {
	t.Helper()
	outputPath := types.StringNull()
	if out != "" {
		outputPath = types.StringValue(out)
	}
	return &passportResourceModel{
		ID:                types.StringValue("agent://acme.example/data/etl-bot"),
		Owner:             types.StringValue("team-data@acme.example"),
		DisplayName:       types.StringValue("ETL bot"),
		Runtime:           types.StringValue("langgraph"),
		Parent:            types.StringNull(),
		AttestationMethod: types.StringValue("spiffe-svid"),
		AttestationDetail: types.StringValue("spiffe://acme.example/data/etl-bot"),
		Labels:            mustLabels(t, map[string]string{"env": "prod"}),
		Filesystem:        mustFilesystem(t, []filesystemScopeModel{{Path: types.StringValue("/data/in"), Mode: types.StringValue("read")}}),
		Models:            mustModels(t, []modelDeclModel{{Provider: types.StringValue("anthropic"), Model: types.StringNull(), Endpoint: types.StringValue("api.anthropic.com")}}),
		OutputPath:        outputPath,
		JSON:              types.StringUnknown(),
	}
}

func passportCreate(t *testing.T, plan *passportResourceModel) (tfsdk.State, *resource.CreateResponse) {
	t.Helper()
	r := &passportResource{}
	s := resourceSchema(t, r)
	resp := &resource.CreateResponse{State: nullState(s)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, resp)
	return resp.State, resp
}

// TestPassportCreateWritesTheFileItPutsInState: the json attribute and the
// file at output_path are the same rendered document (file plus a trailing
// newline), and that document parses as a valid passport carrying the plan.
func TestPassportCreateWritesTheFileItPutsInState(t *testing.T) {
	out := filepath.Join(t.TempDir(), "etl-bot.json")
	st, resp := passportCreate(t, passportModel(t, out))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	var got passportResourceModel
	decodeState(t, st, &got)
	onDisk, err := os.ReadFile(out) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("output_path not written: %v", err)
	}
	if string(onDisk) != got.JSON.ValueString()+"\n" {
		t.Errorf("file and json attribute disagree:\nfile: %s\njson: %s", onDisk, got.JSON.ValueString())
	}
	p, err := passport.Parse([]byte(got.JSON.ValueString()))
	if err != nil {
		t.Fatalf("json attribute is not a valid passport: %v", err)
	}
	if p.ID != "agent://acme.example/data/etl-bot" || p.Owner != "team-data@acme.example" {
		t.Errorf("passport = %+v", p)
	}
}

// TestPassportCreateWithoutOutputPathTouchesNoFile: output_path is optional;
// left unset, Create renders json into state and writes nothing.
func TestPassportCreateWithoutOutputPathTouchesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st, resp := passportCreate(t, passportModel(t, ""))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	var got passportResourceModel
	decodeState(t, st, &got)
	if !strings.Contains(got.JSON.ValueString(), `"agent://acme.example/data/etl-bot"`) {
		t.Errorf("json = %s", got.JSON.ValueString())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Create without output_path wrote %d files", len(entries))
	}
}

// TestPassportCreateRefusesAnInvalidPassport: an owner left empty is refused
// at render time, with nothing written and nothing in state.
func TestPassportCreateRefusesAnInvalidPassport(t *testing.T) {
	out := filepath.Join(t.TempDir(), "bad.json")
	m := passportModel(t, out)
	m.Owner = types.StringValue("")
	st, resp := passportCreate(t, m)
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create accepted a passport with no owner")
	}
	if resp.Diagnostics[0].Summary() != "Invalid taipan_agent_passport" {
		t.Errorf("summary = %q", resp.Diagnostics[0].Summary())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("an invalid passport reached disk (stat err %v)", err)
	}
	if !st.Raw.IsNull() {
		t.Error("an invalid passport reached state")
	}
}

// TestPassportCreateReportsAnUnwritableOutputPath: a missing parent
// directory is an error, not a silent mkdir and not a quiet success.
func TestPassportCreateReportsAnUnwritableOutputPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "missing", "etl-bot.json")
	st, resp := passportCreate(t, passportModel(t, out))
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create succeeded with an output_path whose directory does not exist")
	}
	if resp.Diagnostics[0].Summary() != "Unable to write taipan_agent_passport output_path" {
		t.Errorf("summary = %q", resp.Diagnostics[0].Summary())
	}
	if !st.Raw.IsNull() {
		t.Error("state was written although the file was not")
	}
}

// TestPassportReadRerendersWithoutTouchingDisk: Read recomputes json from
// the other attributes in state (a stale json value is replaced) and does
// not read, rewrite or recreate the file.
func TestPassportReadRerendersWithoutTouchingDisk(t *testing.T) {
	r := &passportResource{}
	s := resourceSchema(t, r)
	out := filepath.Join(t.TempDir(), "never-written.json")
	prior := passportModel(t, out)
	prior.JSON = types.StringValue("stale")
	st := stateOf(t, s, prior)

	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	var got passportResourceModel
	decodeState(t, resp.State, &got)
	if got.JSON.ValueString() == "stale" || !strings.Contains(got.JSON.ValueString(), "etl-bot") {
		t.Errorf("json = %q, want a fresh render", got.JSON.ValueString())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("Read wrote output_path")
	}

	bad := passportModel(t, "")
	bad.ID = types.StringValue("not-an-agent-uri")
	bad.JSON = types.StringValue("x")
	badState := stateOf(t, s, bad)
	resp = resource.ReadResponse{State: badState}
	r.Read(context.Background(), resource.ReadRequest{State: badState}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("Read re-rendered an invalid passport without error")
	}
}

func passportUpdate(t *testing.T, prior, plan *passportResourceModel) (tfsdk.State, *resource.UpdateResponse) {
	t.Helper()
	r := &passportResource{}
	s := resourceSchema(t, r)
	st := stateOf(t, s, prior)
	resp := &resource.UpdateResponse{State: st}
	r.Update(context.Background(), resource.UpdateRequest{Plan: planOf(t, s, plan), State: st}, resp)
	return resp.State, resp
}

// TestPassportUpdateMovingTheOutputPathLeavesNoStaleCopy: moving output_path
// removes the old file and writes the new one; the old copy would otherwise
// sit on disk as a second, unmanaged passport.
func TestPassportUpdateMovingTheOutputPathLeavesNoStaleCopy(t *testing.T) {
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prior := passportModel(t, oldPath)
	prior.JSON = types.StringValue("{}")
	plan := passportModel(t, newPath)
	plan.DisplayName = types.StringValue("ETL bot v2")

	st, resp := passportUpdate(t, prior, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("the old output_path is still on disk after the move")
	}
	onDisk, err := os.ReadFile(newPath) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("new output_path not written: %v", err)
	}
	var got passportResourceModel
	decodeState(t, st, &got)
	if string(onDisk) != got.JSON.ValueString()+"\n" || !strings.Contains(string(onDisk), "ETL bot v2") {
		t.Errorf("new file = %s, want the updated render that is in state", onDisk)
	}
}

// TestPassportUpdateDroppingOutputPathRemovesTheFile: unsetting output_path
// removes the file it used to manage and writes nothing new.
func TestPassportUpdateDroppingOutputPathRemovesTheFile(t *testing.T) {
	oldPath := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prior := passportModel(t, oldPath)
	prior.JSON = types.StringValue("{}")

	_, resp := passportUpdate(t, prior, passportModel(t, ""))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("the file stayed behind after output_path was unset")
	}
}

// TestPassportUpdateFailuresSayWhichStep covers the three ways Update stops:
// an invalid plan (nothing on disk changes), an old file that cannot be
// removed, and a new path that cannot be written.
func TestPassportUpdateFailuresSayWhichStep(t *testing.T) {
	t.Run("invalid plan keeps the old file", func(t *testing.T) {
		oldPath := filepath.Join(t.TempDir(), "old.json")
		if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		prior := passportModel(t, oldPath)
		prior.JSON = types.StringValue("{}")
		plan := passportModel(t, filepath.Join(filepath.Dir(oldPath), "new.json"))
		plan.Owner = types.StringValue("")

		_, resp := passportUpdate(t, prior, plan)
		if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Invalid taipan_agent_passport" {
			t.Fatalf("diagnostics = %v", resp.Diagnostics)
		}
		if _, err := os.Stat(oldPath); err != nil {
			t.Error("an update refused at render time still removed the old file")
		}
	})

	t.Run("old path cannot be removed", func(t *testing.T) {
		// A non-empty directory at the old path: os.Remove fails with
		// something other than not-exist.
		oldPath := filepath.Join(t.TempDir(), "old.json")
		if err := os.MkdirAll(filepath.Join(oldPath, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
		prior := passportModel(t, oldPath)
		prior.JSON = types.StringValue("{}")

		_, resp := passportUpdate(t, prior, passportModel(t, ""))
		if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Unable to remove previous taipan_agent_passport output_path" {
			t.Fatalf("diagnostics = %v", resp.Diagnostics)
		}
	})

	t.Run("new path cannot be written", func(t *testing.T) {
		prior := passportModel(t, "")
		prior.JSON = types.StringValue("{}")
		plan := passportModel(t, filepath.Join(t.TempDir(), "missing", "new.json"))

		_, resp := passportUpdate(t, prior, plan)
		if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Unable to write taipan_agent_passport output_path" {
			t.Fatalf("diagnostics = %v", resp.Diagnostics)
		}
	})
}

// TestPassportDeleteRemovesOnlyWhatItManages: Delete removes the file at
// output_path, tolerates it already being gone, does nothing without an
// output_path, and reports a removal that fails for any other reason.
func TestPassportDeleteRemovesOnlyWhatItManages(t *testing.T) {
	r := &passportResource{}
	s := resourceSchema(t, r)
	del := func(m *passportResourceModel) *resource.DeleteResponse {
		m.JSON = types.StringValue("{}")
		st := stateOf(t, s, m)
		resp := &resource.DeleteResponse{State: st}
		r.Delete(context.Background(), resource.DeleteRequest{State: st}, resp)
		return resp
	}

	dir := t.TempDir()
	managed := filepath.Join(dir, "managed.json")
	if err := os.WriteFile(managed, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resp := del(passportModel(t, managed)); resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Error("Delete left the managed file on disk")
	}

	if resp := del(passportModel(t, managed)); resp.Diagnostics.HasError() {
		t.Errorf("Delete of an already-removed file failed: %v", resp.Diagnostics)
	}

	bystander := filepath.Join(dir, "bystander.json")
	if err := os.WriteFile(bystander, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if resp := del(passportModel(t, "")); resp.Diagnostics.HasError() {
		t.Errorf("Delete without output_path failed: %v", resp.Diagnostics)
	}
	emptyPath := passportModel(t, "")
	emptyPath.OutputPath = types.StringValue("")
	if resp := del(emptyPath); resp.Diagnostics.HasError() {
		t.Errorf("Delete with an empty output_path failed: %v", resp.Diagnostics)
	}
	if _, err := os.Stat(bystander); err != nil {
		t.Error("Delete without output_path removed an unrelated file")
	}

	stuck := filepath.Join(dir, "stuck.json")
	if err := os.MkdirAll(filepath.Join(stuck, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	resp := del(passportModel(t, stuck))
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Unable to remove taipan_agent_passport output_path" {
		t.Errorf("diagnostics = %v, want the removal failure reported", resp.Diagnostics)
	}
}

// TestPassportImportRoundTripsAFileCreateWrote: importing the file Create
// wrote yields the same attributes and the same json, with output_path
// pointing at the imported file. This is the unit-level half of "an import
// round-trips"; the acceptance suite owns the plan-is-empty half.
func TestPassportImportRoundTripsAFileCreateWrote(t *testing.T) {
	out := filepath.Join(t.TempDir(), "etl-bot.json")
	created, cresp := passportCreate(t, passportModel(t, out))
	if cresp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", cresp.Diagnostics)
	}
	var want passportResourceModel
	decodeState(t, created, &want)

	r := &passportResource{}
	resp := resource.ImportStateResponse{State: nullState(resourceSchema(t, r))}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: out}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var got passportResourceModel
	decodeState(t, resp.State, &got)

	if got.JSON.ValueString() != want.JSON.ValueString() {
		t.Errorf("imported json differs from created json:\n got %s\nwant %s", got.JSON.ValueString(), want.JSON.ValueString())
	}
	for name, pair := range map[string][2]types.String{
		"id": {got.ID, want.ID}, "owner": {got.Owner, want.Owner}, "display_name": {got.DisplayName, want.DisplayName},
		"runtime": {got.Runtime, want.Runtime}, "parent": {got.Parent, want.Parent},
		"attestation_method": {got.AttestationMethod, want.AttestationMethod},
		"attestation_detail": {got.AttestationDetail, want.AttestationDetail},
		"output_path":        {got.OutputPath, want.OutputPath},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Errorf("%s: imported %v, created %v", name, pair[0], pair[1])
		}
	}
	if !got.Labels.Equal(want.Labels) || !got.Filesystem.Equal(want.Filesystem) || !got.Models.Equal(want.Models) {
		t.Errorf("labels/filesystem/models differ after import:\n got %v %v %v\nwant %v %v %v",
			got.Labels, got.Filesystem, got.Models, want.Labels, want.Filesystem, want.Models)
	}
}

// TestPassportImportOfAMinimalFileLeavesOptionalsNull: a file with only the
// required fields imports with every optional attribute null (not ""), so
// the first plan after import shows no diff for fields nobody set.
func TestPassportImportOfAMinimalFileLeavesOptionalsNull(t *testing.T) {
	m := passportModel(t, filepath.Join(t.TempDir(), "min.json"))
	m.DisplayName, m.Runtime = types.StringNull(), types.StringNull()
	m.AttestationMethod, m.AttestationDetail = types.StringNull(), types.StringNull()
	m.Labels = types.MapNull(types.StringType)
	m.Filesystem = types.ListNull(filesystemBlockType)
	m.Models = types.ListNull(modelBlockType)
	if _, resp := passportCreate(t, m); resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	r := &passportResource{}
	resp := resource.ImportStateResponse{State: nullState(resourceSchema(t, r))}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: m.OutputPath.ValueString()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var got passportResourceModel
	decodeState(t, resp.State, &got)
	for name, v := range map[string]types.String{
		"display_name": got.DisplayName, "runtime": got.Runtime, "parent": got.Parent,
		"attestation_method": got.AttestationMethod, "attestation_detail": got.AttestationDetail,
	} {
		if !v.IsNull() {
			t.Errorf("%s = %v, want null", name, v)
		}
	}
	if !got.Labels.IsNull() {
		t.Errorf("labels = %v, want null", got.Labels)
	}
}

// TestPassportImportRefusesWhatIsNotAPassportFile: a missing file and a file
// that is not a valid passport are both refused with a message naming the
// path, and nothing reaches state.
func TestPassportImportRefusesWhatIsNotAPassportFile(t *testing.T) {
	dir := t.TempDir()
	notPassport := filepath.Join(dir, "not-a-passport.json")
	if err := os.WriteFile(notPassport, []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ path, summary string }{
		"missing":     {filepath.Join(dir, "nope.json"), "Unable to read taipan_agent_passport import file"},
		"not a valid": {notPassport, "Invalid taipan_agent_passport import file"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := &passportResource{}
			resp := resource.ImportStateResponse{State: nullState(resourceSchema(t, r))}
			r.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.path}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("import accepted it")
			}
			if resp.Diagnostics[0].Summary() != tc.summary {
				t.Errorf("summary = %q, want %q", resp.Diagnostics[0].Summary(), tc.summary)
			}
			if !strings.Contains(resp.Diagnostics[0].Detail(), tc.path) {
				t.Errorf("detail does not name the path: %q", resp.Diagnostics[0].Detail())
			}
			if !resp.State.Raw.IsNull() {
				t.Error("a refused import reached state")
			}
		})
	}
}

// TestPassportRejectsUndecodablePlanAndState: every lifecycle method stops
// at a plan or state of the wrong shape, before touching disk.
func TestPassportRejectsUndecodablePlanAndState(t *testing.T) {
	r := &passportResource{}
	own := resourceSchema(t, r)
	otherSchema := resourceSchema(t, &budgetResource{})
	other := &budgetResourceModel{RunID: types.StringValue("x"), LimitUSD: types.Float64Value(1)}
	wrongPlan := planOf(t, otherSchema, other)
	wrongState := stateOf(t, otherSchema, other)
	ctx := context.Background()

	c := resource.CreateResponse{State: nullState(own)}
	r.Create(ctx, resource.CreateRequest{Plan: wrongPlan}, &c)
	rd := resource.ReadResponse{State: wrongState}
	r.Read(ctx, resource.ReadRequest{State: wrongState}, &rd)
	u1 := resource.UpdateResponse{State: wrongState}
	r.Update(ctx, resource.UpdateRequest{Plan: wrongPlan, State: wrongState}, &u1)
	goodPrior := passportModel(t, "")
	goodPrior.JSON = types.StringValue("{}")
	goodPlan := planOf(t, own, passportModel(t, ""))
	u2 := resource.UpdateResponse{State: wrongState}
	r.Update(ctx, resource.UpdateRequest{Plan: goodPlan, State: wrongState}, &u2)
	d := resource.DeleteResponse{State: wrongState}
	r.Delete(ctx, resource.DeleteRequest{State: wrongState}, &d)

	for name, failed := range map[string]bool{
		"Create": c.Diagnostics.HasError(), "Read": rd.Diagnostics.HasError(),
		"Update plan": u1.Diagnostics.HasError(), "Update state": u2.Diagnostics.HasError(),
		"Delete": d.Diagnostics.HasError(),
	} {
		if !failed {
			t.Errorf("%s decoded a value of the wrong shape without error", name)
		}
	}
}
