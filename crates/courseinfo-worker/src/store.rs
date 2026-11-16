use std::collections::BTreeMap;

use courseinfo_core::{Course, Error, course_department, normalize_course, serialize_department};
use serde::Deserialize;
use worker::{D1Database, wasm_bindgen::JsValue};

use crate::error::{ApiError, Result};

pub(crate) enum Lookup {
    NoDepartment,
    Missing,
    Found(Course),
}

#[derive(Deserialize)]
struct LookupRow {
    course_json: Option<String>,
    course_type: Option<String>,
}

#[derive(Deserialize)]
struct CourseRow {
    code: String,
    department: String,
    course_json: String,
}

fn decode_course(json: &str, code: &str, department: &str) -> Result<Course> {
    let course: Course = serde_json::from_str(json).map_err(ApiError::internal)?;
    if course.code != code
        || normalize_course(&course.code).map_err(ApiError::internal)? != course.code
        || course_department(&course.code).map_err(ApiError::internal)? != department
        || course.title.trim().is_empty()
        || !course.credits.is_finite()
        || course.credits < 0.0
    {
        return Err(ApiError::internal("invalid stored course"));
    }
    Ok(course)
}

pub(crate) async fn lookup(
    db: &D1Database,
    semester: &str,
    department: &str,
    code: &str,
) -> Result<Lookup> {
    let path = format!("$.\"{code}\"");
    let row = db.prepare(
        "SELECT json_extract(courses_json, ?3) AS course_json, json_type(courses_json, ?3) AS course_type \
         FROM department_snapshots WHERE semester = ?1 AND department = ?2"
    ).bind(&[semester.into(), department.into(), path.into()]).map_err(ApiError::internal)?
        .first::<LookupRow>(None).await.map_err(ApiError::internal)?;
    match row {
        None => Ok(Lookup::NoDepartment),
        Some(LookupRow {
            course_type: None, ..
        }) => Ok(Lookup::Missing),
        Some(LookupRow {
            course_json: Some(json),
            course_type: Some(kind),
        }) if kind == "object" => decode_course(&json, code, department).map(Lookup::Found),
        _ => Err(ApiError::internal("invalid stored course JSON")),
    }
}

pub(crate) async fn list(db: &D1Database, semester: &str) -> Result<Option<Vec<Course>>> {
    let result = db
        .prepare(
            "SELECT entry.key AS code, snapshots.department, entry.value AS course_json \
         FROM department_snapshots AS snapshots, json_each(snapshots.courses_json) AS entry \
         WHERE snapshots.semester = ?1 ORDER BY entry.key",
        )
        .bind(&[semester.into()])
        .map_err(ApiError::internal)?
        .all()
        .await
        .map_err(ApiError::internal)?;
    let rows = result.results::<CourseRow>().map_err(ApiError::internal)?;
    if rows.is_empty() {
        return Ok(None);
    }
    rows.into_iter()
        .map(|row| decode_course(&row.course_json, &row.code, &row.department))
        .collect::<Result<Vec<_>>>()
        .map(Some)
}

pub(crate) async fn publish(
    db: &D1Database,
    semester: &str,
    department: &str,
    observed_at_ms: i64,
    courses: &BTreeMap<String, Course>,
) -> Result<()> {
    let json = serialize_department(courses).map_err(|error| {
        if error == Error::SnapshotTooLarge {
            ApiError::upstream(error.to_string())
        } else {
            ApiError::internal(error)
        }
    })?;
    db.prepare(include_str!("../../../db/queries/publish.sql"))
        .bind(&[
            semester.into(),
            department.into(),
            JsValue::from_f64(observed_at_ms as f64),
            json.into(),
        ])
        .map_err(ApiError::internal)?
        .run()
        .await
        .map_err(ApiError::internal)?;
    Ok(())
}

pub(crate) async fn finish_refresh(
    db: &D1Database,
    semester: &str,
    started_at_ms: i64,
    run_id: &str,
    departments: &[String],
) -> Result<()> {
    let advance = db
        .prepare(include_str!("../../../db/queries/advance.sql"))
        .bind(&[
            semester.into(),
            JsValue::from_f64(started_at_ms as f64),
            run_id.into(),
        ])
        .map_err(ApiError::internal)?;
    let index = serde_json::to_string(departments).map_err(ApiError::internal)?;
    let prune = db
        .prepare(include_str!("../../../db/queries/prune.sql"))
        .bind(&[
            semester.into(),
            JsValue::from_f64(started_at_ms as f64),
            run_id.into(),
            index.into(),
        ])
        .map_err(ApiError::internal)?;
    db.batch(vec![advance, prune])
        .await
        .map_err(ApiError::internal)?;
    Ok(())
}
