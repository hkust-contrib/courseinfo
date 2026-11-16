INSERT INTO semester_publications (semester, cutoff_ms, run_id) VALUES (?1, ?2, ?3)
ON CONFLICT(semester) DO UPDATE SET
    cutoff_ms = excluded.cutoff_ms,
    run_id = excluded.run_id
WHERE excluded.cutoff_ms > semester_publications.cutoff_ms
