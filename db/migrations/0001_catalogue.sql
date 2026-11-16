CREATE TABLE department_snapshots (
    semester TEXT NOT NULL,
    department TEXT NOT NULL,
    observed_at_ms INTEGER NOT NULL,
    courses_json TEXT NOT NULL CHECK (json_valid(courses_json) AND json_type(courses_json) = 'object'),
    PRIMARY KEY (semester, department)
) WITHOUT ROWID;

CREATE TABLE semester_publications (
    semester TEXT PRIMARY KEY NOT NULL,
    cutoff_ms INTEGER NOT NULL,
    run_id TEXT NOT NULL
) WITHOUT ROWID;
