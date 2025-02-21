package exporter

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/databricks/terraform-provider-databricks/provider"
	"github.com/databricks/terraform-provider-databricks/qa"
	"github.com/databricks/terraform-provider-databricks/workspace"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoServicesSkipsRun(t *testing.T) {
	assert.EqualError(t, (&importContext{}).Run(), "no services to import")
}

func TestMatchesName(t *testing.T) {
	assert.False(t, (&importContext{match: "x"}).MatchesName("y"))
}

func TestImportContextFindSkips(t *testing.T) {
	state := newStateApproximation([]string{"a"})
	state.Append(resourceApproximation{
		Type: "a",
		Instances: []instanceApproximation{
			{
				Attributes: map[string]any{
					"b": nil,
				},
			},
		}})
	_, traversal := (&importContext{
		State: state,
	}).Find(&resource{
		Resource:  "a",
		Attribute: "b",
		Name:      "c",
	}, "x", reference{})
	assert.Nil(t, traversal)
}

func TestImportContextHas(t *testing.T) {
	state := newStateApproximation([]string{"a"})
	state.Append(resourceApproximation{
		Type: "a",
		Instances: []instanceApproximation{
			{
				Attributes: map[string]any{
					"b": "d",
				},
			},
		}})
	assert.True(t, (&importContext{State: state}).Has(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	}))
}

func TestEmitNaResource(t *testing.T) {
	state := newStateApproximation([]string{"a"})
	(&importContext{
		importing: map[string]bool{},
		State:     state,
	}).Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
}

func TestEmitNoImportable(t *testing.T) {
	state := newStateApproximation([]string{"a"})
	(&importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		State: state,
	}).Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
}

func TestEmitNoSearchAvail(t *testing.T) {
	ch := make(resourceChannel)
	state := newStateApproximation([]string{"a"})
	ic := &importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		Importables: map[string]importable{
			"a": {
				Service: "e",
			},
		},
		waitGroup: &sync.WaitGroup{},
		channels: map[string]resourceChannel{
			"a": ch,
		},
		ignoredResources: map[string]struct{}{},
		State:            state,
	}
	ic.enableServices("e")
	go func() {
		for r := range ch {
			r.ImportResource(ic)
		}
	}()
	ic.Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
	ic.waitGroup.Wait()
	close(ch)
}

func TestEmitNoSearchFails(t *testing.T) {
	ch := make(resourceChannel, 10)
	state := newStateApproximation([]string{"a"})
	ic := &importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		Importables: map[string]importable{
			"a": {
				Service: "e",
				Search: func(ic *importContext, r *resource) error {
					return fmt.Errorf("just fails")
				},
			},
		},
		waitGroup: &sync.WaitGroup{},
		channels: map[string]resourceChannel{
			"a": ch,
		},
		State: state,
	}
	ic.enableServices("e")
	go func() {
		for r := range ch {
			r.ImportResource(ic)
		}
	}()
	ic.Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
	ic.waitGroup.Wait()
	close(ch)
}

func TestEmitNoSearchNoId(t *testing.T) {
	ch := make(resourceChannel, 10)
	state := newStateApproximation([]string{"a"})
	ic := &importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		Importables: map[string]importable{
			"a": {
				Service: "e",
				Search: func(ic *importContext, r *resource) error {
					return nil
				},
			},
		},
		waitGroup: &sync.WaitGroup{},
		channels: map[string]resourceChannel{
			"a": ch,
		},
		ignoredResources: map[string]struct{}{},
		State:            state,
	}
	ic.enableServices("e")
	go func() {
		for r := range ch {
			r.ImportResource(ic)
		}
	}()
	ic.Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
	ic.waitGroup.Wait()
	close(ch)
}

func TestEmitNoSearchNoIdWithRetry(t *testing.T) {
	ch := make(resourceChannel, 10)
	state := newStateApproximation([]string{"a"})
	i := 0
	ic := &importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		Importables: map[string]importable{
			"a": {
				Service: "e",
				Search: func(ic *importContext, r *resource) error {
					if i > 0 {
						return nil
					}
					i = i + 1
					return fmt.Errorf("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
				},
			},
		},
		waitGroup: &sync.WaitGroup{},
		channels: map[string]resourceChannel{
			"a": ch,
		},
		ignoredResources: map[string]struct{}{},
		State:            state,
	}
	ic.enableServices("e")
	go func() {
		for r := range ch {
			r.ImportResource(ic)
		}
	}()
	ic.Emit(&resource{
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
	ic.waitGroup.Wait()
	close(ch)
}

func TestEmitNoSearchSucceedsImportFails(t *testing.T) {
	ch := make(resourceChannel, 10)
	state := newStateApproximation([]string{"a"})
	ic := &importContext{
		importing: map[string]bool{},
		Resources: map[string]*schema.Resource{
			"a": {},
		},
		Importables: map[string]importable{
			"a": {
				Service: "e",
				Search: func(ic *importContext, r *resource) error {
					r.ID = "some"
					return nil
				},
				Import: func(ic *importContext, r *resource) error {
					return fmt.Errorf("fails")
				},
			},
		},
		waitGroup: &sync.WaitGroup{},
		channels: map[string]resourceChannel{
			"a": ch,
		},
		State: state,
	}
	ic.enableServices("e")
	go func() {
		for r := range ch {
			r.ImportResource(ic)
		}
	}()
	ic.Emit(&resource{
		Data:      &schema.ResourceData{},
		Resource:  "a",
		Attribute: "b",
		Value:     "d",
		Name:      "c",
	})
	ic.waitGroup.Wait()
	close(ch)
}

func TestLoadingLastRun(t *testing.T) {
	tmpDir := fmt.Sprintf("/tmp/tf-%s", qa.RandomName())
	defer os.RemoveAll(tmpDir)

	fname := tmpDir + "1.json"
	// no file yet
	s := getLastRunString(fname)
	assert.Equal(t, "", s)

	_ = os.WriteFile(fname, []byte("{"), 0755)
	s = getLastRunString(fname)
	assert.Equal(t, "", s)

	// no required field
	_ = os.WriteFile(fname, []byte("{}"), 0755)
	s = getLastRunString(fname)
	assert.Equal(t, "", s)

	_ = os.WriteFile(fname, []byte(`{"startTime": "2023-07-24T00:00:00Z"}`), 0755)
	s = getLastRunString(fname)
	assert.Equal(t, "2023-07-24T00:00:00Z", s)
}

func TestGenerateResourceIdForWsObject(t *testing.T) {
	p := provider.DatabricksProvider()
	ic := &importContext{
		Importables: resourcesMap,
		Resources:   p.ResourcesMap,
	}
	rid, rtype := ic.generateResourceIdForWsObject(workspace.ObjectStatus{
		ObjectID:   123,
		Path:       "Test",
		ObjectType: "Unknown",
	})
	assert.Empty(t, rid)
	assert.Empty(t, rtype)

	rid, rtype = ic.generateResourceIdForWsObject(workspace.ObjectStatus{
		ObjectID:   123,
		Path:       "/Users/user@domain.com/TestDir",
		ObjectType: workspace.Directory,
	})
	assert.Equal(t, "databricks_directory.users_user_domain_com_testdir_123", rid)
	assert.Equal(t, "databricks_directory", rtype)

	rid, rtype = ic.generateResourceIdForWsObject(workspace.ObjectStatus{
		ObjectID:   123,
		Path:       "/Users/user@domain.com/Test File",
		ObjectType: workspace.File,
	})
	assert.Equal(t, "databricks_workspace_file.users_user_domain_com_test_file_123", rid)
	assert.Equal(t, "databricks_workspace_file", rtype)

	rid, rtype = ic.generateResourceIdForWsObject(workspace.ObjectStatus{
		ObjectID:   123,
		Path:       "/Users/user@domain.com/Test Notebook",
		ObjectType: workspace.Notebook,
	})
	assert.Equal(t, "databricks_notebook.users_user_domain_com_test_notebook_123", rid)
	assert.Equal(t, "databricks_notebook", rtype)
}

func TestDeletedWsObjectsDetection(t *testing.T) {
	ic := importContextForTest()
	ic.incremental = true

	tmpDir := fmt.Sprintf("/tmp/tf-%s", qa.RandomName())
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	objects := []workspace.ObjectStatus{
		{ObjectID: 123, ObjectType: "REPO", Path: "/Repos/user@domain.com/test"},
		{ObjectID: 456, ObjectType: "NOTEBOOK", Path: "/Test/1234"},
		// This is deleted objects
		{ObjectID: 789, ObjectType: "DIRECTORY", Path: "/Test/TDir"},
		{ObjectID: 12, ObjectType: "FILE", Path: "/Test/TDir"},
		{ObjectID: 345, ObjectType: "NOTEBOOK", Path: "/Test/12345"},
	}

	bytes, _ := json.Marshal(objects)
	fname := tmpDir + "/1.json"
	os.WriteFile(fname, bytes, 0755)

	ic.loadOldWorkspaceObjects(fname)
	ic.allWorkspaceObjects = objects[0:2]
	ic.findDeletedResources()
	require.Equal(t, 6, len(ic.deletedResources))
	assert.Contains(t, ic.deletedResources, "databricks_directory.test_tdir_789")
	assert.Contains(t, ic.deletedResources, "databricks_permissions.directory_test_tdir_789")
	assert.Contains(t, ic.deletedResources, "databricks_notebook.test_12345_345")
	assert.Contains(t, ic.deletedResources, "databricks_permissions.notebook_test_12345_345")
	assert.Contains(t, ic.deletedResources, "databricks_workspace_file.test_tdir_12")
	assert.Contains(t, ic.deletedResources, "databricks_permissions.ws_file_test_tdir_12")

	// errors/edge case handling
	_ = os.WriteFile(fname, []byte("[]"), 0755)
	ic.loadOldWorkspaceObjects(fname)
	require.Equal(t, 0, len(ic.oldWorkspaceObjects))
	ic.findDeletedResources()

	// Incorrect data type
	_ = os.WriteFile(fname, []byte("{}"), 0755)
	ic.loadOldWorkspaceObjects(fname)
	require.Equal(t, 0, len(ic.oldWorkspaceObjects))
}
