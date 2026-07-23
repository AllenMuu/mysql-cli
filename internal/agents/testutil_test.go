package agents

import "testing/fstest"

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"mysql-shared/SKILL.md": {Data: []byte("---\nname: mysql-shared\nversion: 1.0.0\n---\n\nshared body\n")},
		"mysql-query/SKILL.md":  {Data: []byte("---\nname: mysql-query\nversion: 1.0.0\n---\n\nquery body\n")},
		"mysql-schema/SKILL.md": {Data: []byte("---\nname: mysql-schema\nversion: 1.0.0\n---\n\nschema body\n")},
	}
}

var testNames = []string{"mysql-shared", "mysql-query", "mysql-schema"}