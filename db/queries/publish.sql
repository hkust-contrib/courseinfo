INSERT INTO department_snapshots (semester, department, observed_at_ms, courses_json)
SELECT ?1, ?2, ?3, ?4
WHERE ?3 > COALESCE((SELECT cutoff_ms FROM semester_publications WHERE semester = ?1), -1)
ON CONFLICT(semester, department) DO UPDATE SET
    observed_at_ms = excluded.observed_at_ms,
    courses_json = excluded.courses_json
WHERE excluded.observed_at_ms > department_snapshots.observed_at_ms
