DELETE FROM department_snapshots
WHERE semester = ?1 AND observed_at_ms <= ?2
AND department NOT IN (SELECT value FROM json_each(?4))
AND EXISTS (SELECT 1 FROM semester_publications WHERE semester = ?1 AND run_id = ?3)
