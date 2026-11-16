use std::net::IpAddr;

use courseinfo_core::{Course, course_department, normalize_course};
use worker::{D1Database, Env, Headers, RateLimiter, Url, js_sys, wasm_bindgen::JsCast};

use crate::{
    error::{ApiError, Result},
    limits::{self, Limits},
    source,
    store::{self, Lookup},
};

pub(crate) struct Catalogue {
    db: D1Database,
    base_url: String,
    limits: Limits,
    miss_limiter: RateLimiter,
}

impl Catalogue {
    pub fn new(env: &Env) -> Result<Self> {
        let base = env.var("BASE_URL").map_err(ApiError::internal)?.to_string();
        let url = Url::parse(&base).map_err(ApiError::internal)?;
        let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
        if !(url.scheme() == "https" || (local && url.scheme() == "http"))
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(ApiError::internal("invalid BASE_URL"));
        }
        Ok(Self {
            db: env.d1("DB").map_err(ApiError::internal)?,
            base_url: url.as_str().trim_end_matches('/').to_owned(),
            limits: Limits::from_env(env)?,
            miss_limiter: env
                .rate_limiter("COURSE_MISS_LIMITER")
                .map_err(ApiError::internal)?,
        })
    }

    pub async fn course(&self, semester: &str, input: &str, headers: &Headers) -> Result<Course> {
        let code = normalize_course(input).map_err(ApiError::invalid)?;
        let department = course_department(&code).map_err(ApiError::invalid)?;
        if let Lookup::Found(course) = store::lookup(&self.db, semester, department, &code).await? {
            return Ok(course);
        }
        // A single budget spans every course/department, so varying URLs cannot bypass it.
        // Worker subrequests share a bucket because same-zone scripts can override client IPs.
        let key = if headers.has("CF-Worker").map_err(ApiError::internal)? {
            "worker-subrequest".to_owned()
        } else {
            headers
                .get("CF-Connecting-IP")
                .map_err(ApiError::internal)?
                .and_then(|value| value.parse::<IpAddr>().ok())
                .map_or_else(|| "unknown-client".to_owned(), |ip| ip.to_string())
        };
        if !self
            .miss_limiter
            .limit(key)
            .await
            .map_err(ApiError::internal)?
            .success
        {
            return Err(ApiError::new(429, "course acquisition rate limit exceeded"));
        }
        let page = source::acquire(
            &self.base_url,
            semester,
            department,
            source::now_ms() + self.limits.fetch_ms as i64,
            self.limits.fetch_ms,
        )
        .await?;
        store::publish(
            &self.db,
            semester,
            department,
            page.observed_at_ms,
            &page.page.courses,
        )
        .await?;
        match store::lookup(&self.db, semester, department, &code).await? {
            Lookup::Found(course) => Ok(course),
            Lookup::Missing => Err(ApiError::new(404, format!("course {code} not found"))),
            Lookup::NoDepartment => Err(ApiError::new(
                503,
                "course lookup was superseded by a newer catalogue refresh",
            )),
        }
    }

    pub async fn list(&self, semester: &str) -> Result<Option<Vec<Course>>> {
        store::list(&self.db, semester).await
    }

    pub async fn refresh(&self, semester: &str) -> Result<()> {
        let started_at = source::now_ms();
        let deadline = started_at + self.limits.refresh_ms as i64;
        let count =
            limits::until(deadline, self.refresh_until(semester, started_at, deadline)).await?;
        worker::console_log!(
            "catalogue refresh completed: semester={} departments={}",
            semester,
            count
        );
        Ok(())
    }

    async fn refresh_until(&self, semester: &str, started_at: i64, deadline: i64) -> Result<usize> {
        let run_id = run_id()?;
        let first = source::acquire(
            &self.base_url,
            semester,
            "COMP",
            deadline,
            self.limits.fetch_ms,
        )
        .await?;
        let departments = first.page.departments;
        if !departments.iter().any(|department| department == "COMP") {
            return Err(ApiError::upstream("catalogue discovery omitted COMP"));
        }
        let mut failure = None;
        if let Err(error) = store::publish(
            &self.db,
            semester,
            "COMP",
            first.observed_at_ms,
            &first.page.courses,
        )
        .await
        {
            failure = Some(error);
        }
        drop(first.page.courses);
        for department in departments
            .iter()
            .filter(|department| department.as_str() != "COMP")
        {
            if source::now_ms() >= deadline {
                failure = Some(ApiError::upstream("catalogue refresh deadline exceeded"));
                break;
            }
            let outcome = async {
                let acquired = source::acquire(
                    &self.base_url,
                    semester,
                    department,
                    deadline,
                    self.limits.fetch_ms,
                )
                .await?;
                store::publish(
                    &self.db,
                    semester,
                    department,
                    acquired.observed_at_ms,
                    &acquired.page.courses,
                )
                .await
            }
            .await;
            if let Err(error) = outcome {
                worker::console_error!(
                    "department refresh failed: semester={} department={} status={}",
                    semester,
                    department,
                    error.status
                );
                if failure
                    .as_ref()
                    .is_none_or(|previous: &ApiError| previous.status != 500)
                {
                    failure = Some(error);
                }
            }
        }
        if let Some(error) = failure {
            return Err(error);
        }
        if source::now_ms() >= deadline {
            return Err(limits::expired());
        }
        store::finish_refresh(&self.db, semester, started_at, &run_id, &departments).await?;
        Ok(departments.len())
    }
}

fn run_id() -> Result<String> {
    let crypto =
        js_sys::Reflect::get(&js_sys::global(), &"crypto".into()).map_err(ApiError::internal)?;
    let random_uuid = js_sys::Reflect::get(&crypto, &"randomUUID".into())
        .map_err(ApiError::internal)?
        .dyn_into::<js_sys::Function>()
        .map_err(ApiError::internal)?;
    random_uuid
        .call0(&crypto)
        .map_err(ApiError::internal)?
        .as_string()
        .ok_or_else(|| ApiError::internal("crypto.randomUUID did not return a string"))
}
