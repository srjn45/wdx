package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// committed is the sha256 of each committed migration, in order. Migrations
// are append-only: add the sum of a new one here, never change one.
var committed = []string{
	"89466dd1bea0a3b0ce3a744e4f04b090f59534b754e57c5db9b69aa37d7a48be", // 0001_projects.sql
	"2dd7fb8b56a1e9034e56639afd054ff676cad79ea236026a282943d01fdb6167", // 0002_schema_readers.sql
	"b3e0c3404c6e719b224d08ffd963bc671ed0c4db321cf97d3e7378caf0fa4d68", // 0003_terminals.sql
	"af5346f10dbf65585b0ec0992edd4df477758fe1cf3607b66cc7319a20e0cd29", // 0004_agents.sql
	"05fbe5f1afb148ef3779586e9b78f133538ff34a55a001e7cb15461d8d31ac12", // 0005_child_agents.sql
	"1765ef2b0e9371a6c7f0de7d5822cbcc2ba855391ec2544c6ccf96b5e485f6e8", // 0006_agent_status.sql
	"a2607b72adc68da7b8424eb685830228cbc320186e2cf2de07dd525a8c13bce9", // 0007_agent_queue.sql
	"d2d92da9f050b74fe714dbf14bdf0b08f93b73ef00dfbe94542290cc648151d0", // 0008_agent_model.sql
	"2e4e277fd6c03620e35c4365a73cc6859b15e087288cd7de56a2b293f7e392ab", // 0009_verified_files.sql
	"1ffcbe30f2c1e24f1d6eb3e8d613610586829a85d8232421af647642a68dfcf2", // 0010_review_comments.sql
	"3031c67286aec73ff8c7ffbdb6c7b561afdbce38ad89dd90f76307d5421730fd", // 0011_agent_current_dir.sql
	"b67efe2130f1d14e98b9e8e58e7ffb382f5e977832e37c462efd98298f0c2d85", // 0012_messages.sql
	"1942a9e3339636b8f32e14cbac6ad3ad49c7c6977f334e7027a8f4f4b9a47e31", // 0013_project_landing.sql
	"64e1beb4c340c30eaa35df2e11759364810fad07ba2f0da7365e0eb7a4837564", // 0014_landing_by_pr.sql
	"36d5321045379fdce424e55f27daf7d69856d0cba022b3d8d842d127ab734e42", // 0015_editor_path.sql
	"cc850278ea1eaab8b75629ca6e9094e4b2fc84173b21f623e499876f57b597ab", // 0016_docs.sql
	"7672c273758e8be59d163d5f9c0900a531fb9e7f17e2a5c321193e48abe41f7a", // 0017_tide_tabs.sql
	"ece96b7d2dd4eaac7443ab2413e8085353a7ba40c3e720de45022f21d8c064d4", // 0018_tide_view.sql
	"55345f40d653ae960db59062dc82317950cad8f9e8d2d075097ca1bed86065d5", // 0019_tide_tab_revisions.sql
	"35b3f41da70f7f7015782217bfb21ef42bd985933e9404f40a75f4c22c057dae", // 0020_plans.sql
	"6e4a1baac0858d6467d75e9bb174545ef8f775948aa307f658979e97ae77b375", // 0021_plan_tabs.sql
	"e7822071bfb740d9eb3f4d9bae3509fcad959abfb64b116210c9c06748ac2661", // 0022_plan_runs.sql
	"ecb659f3108ff9810f27be2d8542bb6ce1367facfeed3596454bfea980fdbadb", // 0023_agent_launch.sql
	"6d03ef5d3dbfa61b2ca57f54e0f79e772a25877be3ab33e6b80f59711686f7a1", // 0024_doc_folders.sql
	"eb1639a3cc82f82e9925e265e9f671947098a8052a57d4f1d90c4a8fbd2d5f3c", // 0025_plan_base_commit.sql
	"ed68b721230dad7785d21ea885a21b5c2bcd172787f64a39b671033a455df680", // 0026_agent_context.sql
}

func TestMigrationsNumbered(t *testing.T) {
	t.Parallel()
	for i, m := range mustMigrations() {
		if want := fmt.Sprintf("migrations/%04d_", i+1); !strings.HasPrefix(m.name, want) {
			t.Errorf("migration %d is %s, want it named %s*.sql", i+1, m.name, want)
		}
	}
}

func TestMigrationsAppendOnly(t *testing.T) {
	t.Parallel()
	list := mustMigrations()
	for i, m := range list {
		sum := sha256.Sum256([]byte(m.body))
		got := hex.EncodeToString(sum[:])
		switch {
		case i >= len(committed):
			t.Errorf("%s is new: add its sum to committed, %q", m.name, got)
		case got != committed[i]:
			t.Errorf("%s was changed: migrations are append-only, add a new one instead", m.name)
		}
	}
	if len(list) < len(committed) {
		t.Errorf("want %d migrations, got %d: migrations are never removed", len(committed), len(list))
	}
}

func TestTablesStrict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.QueryContext(ctx, "SELECT name FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND NOT strict AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("table %s is not STRICT", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
