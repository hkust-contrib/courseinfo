mod catalogue;
mod error;
mod limits;
mod source;
mod store;

use std::{cell::Cell, net::Ipv4Addr, panic::AssertUnwindSafe};

use courseinfo_core::{parse_semester, semester_for_timestamp};
use serde::Serialize;
use wasm_bindgen::prelude::wasm_bindgen;
use worker::{
    Context, Env, Method, Request, Response, ScheduledEvent, event, js_sys,
    wasm_bindgen::{self, JsValue},
};

use catalogue::Catalogue;
use error::{ApiError, Result};

const WEEKLY_CRON: &str = "0 19 * * SUN";
thread_local! {
    static FIRST_EVENT_MS: Cell<Option<i64>> = const { Cell::new(None) };
}

fn event_time() -> i64 {
    let now = source::now_ms();
    FIRST_EVENT_MS.with(|first| {
        if first.get().is_none() {
            first.set(Some(now));
        }
    });
    now
}

#[event(fetch)]
pub async fn fetch(request: Request, env: Env, _context: Context) -> worker::Result<Response> {
    let now = event_time();
    let mut response = match dispatch(request, &env, now).await {
        Ok(response) => response,
        Err(error) => error.response()?,
    };
    response.headers_mut().set("Cache-Control", "no-store")?;
    Ok(response)
}

async fn dispatch(request: Request, env: &Env, now: i64) -> Result<Response> {
    let url = request.url().map_err(ApiError::invalid)?;
    let path = url.path().trim_end_matches('/');
    let method = request.method();
    match (method.clone(), path) {
        (Method::Get, "") => {
            let mut response = Response::empty()
                .map_err(ApiError::internal)?
                .with_status(301);
            response
                .headers_mut()
                .set("Location", "/v1")
                .map_err(ApiError::internal)?;
            Ok(response)
        }
        (Method::Get, "/healthz") => json(&serde_json::json!({"status": "ok"})),
        (Method::Get, "/v1") => introspection(env, now),
        (Method::Get, "/v1/courses") => {
            let semester = semester_for_timestamp(now).map_err(ApiError::internal)?;
            json(&Catalogue::new(env)?.list(&semester).await?)
        }
        (Method::Patch, "/v1/courses") => {
            require_refresh_network(&request)?;
            let semester = semester_for_timestamp(now).map_err(ApiError::internal)?;
            let catalogue = Catalogue::new(env)?;
            catalogue.refresh(&semester).await?;
            json(&catalogue.list(&semester).await?)
        }
        _ => {
            if let Some(code) = path
                .strip_prefix("/v1/semesters/")
                .filter(|code| !code.is_empty() && !code.contains('/'))
            {
                require_get(method)?;
                let semester = if code == "current" {
                    parse_semester(&semester_for_timestamp(now).map_err(ApiError::internal)?)
                        .map_err(ApiError::internal)?
                } else {
                    parse_semester(code).map_err(ApiError::invalid)?
                };
                return json(&semester);
            }
            if let Some(code) = path
                .strip_prefix("/v1/courses/")
                .filter(|code| !code.is_empty() && !code.contains('/'))
            {
                require_get(method)?;
                let semester = semester_for_timestamp(now).map_err(ApiError::internal)?;
                return json(
                    &Catalogue::new(env)?
                        .course(&semester, code, request.headers())
                        .await?,
                );
            }
            if matches!(path, "" | "/healthz" | "/v1" | "/v1/courses") {
                return Err(ApiError::method_not_allowed(if path == "/v1/courses" {
                    "GET, PATCH"
                } else {
                    "GET"
                }));
            }
            Err(ApiError::new(404, "route not found"))
        }
    }
}

fn require_refresh_network(request: &Request) -> Result<()> {
    let headers = request.headers();
    let allowed = headers
        .get("CF-Connecting-IP")
        .map_err(ApiError::internal)?
        .and_then(|value| value.parse::<Ipv4Addr>().ok())
        .is_some_and(|ip| matches!(ip.octets(), [143, 89, _, _]));
    // Same-zone Worker subrequests can override CF-Connecting-IP via x-real-ip.
    if !allowed || headers.has("CF-Worker").map_err(ApiError::internal)? {
        return Err(ApiError::new(
            403,
            "refresh requires a direct client in 143.89.0.0/16",
        ));
    }
    Ok(())
}

fn require_get(method: Method) -> Result<()> {
    if method != Method::Get {
        return Err(ApiError::method_not_allowed("GET"));
    }
    Ok(())
}

fn json<T: Serialize>(value: &T) -> Result<Response> {
    Response::from_json(value).map_err(ApiError::internal)
}

fn introspection(env: &Env, now: i64) -> Result<Response> {
    #[derive(Serialize)]
    struct Metadata {
        runtime: &'static str,
        hostname: String,
        platform: &'static str,
        build_commit: &'static str,
        build_date: &'static str,
        uptime: String,
    }
    let elapsed = FIRST_EVENT_MS.with(|first| now.saturating_sub(first.get().unwrap_or(now)));
    json(&Metadata {
        runtime: env!("RUSTC_VERSION"),
        hostname: env
            .var("WORKER_NAME")
            .map_err(ApiError::internal)?
            .to_string(),
        platform: "cloudflare-workers wasm32",
        build_commit: env!("BUILD_COMMIT"),
        build_date: env!("BUILD_DATE"),
        uptime: format!("{:.2}", elapsed.max(0) as f64 / 1000.0),
    })
}

// worker 0.8.7's scheduled macro discards the handler's Result. Return and
// register the same rejecting promise so Cron records refresh failures.
#[wasm_bindgen]
pub fn scheduled(
    event: worker::worker_sys::ScheduledEvent,
    env: Env,
    context: worker::worker_sys::ScheduleContext,
) -> std::result::Result<js_sys::Promise, JsValue> {
    let now = event_time();
    let promise = js_sys::futures::future_to_promise(AssertUnwindSafe(async move {
        let event = ScheduledEvent::from(event);
        let result: Result<()> = async {
            if event.cron() != WEEKLY_CRON {
                return Err(ApiError::new(400, "unrecognized catalogue schedule"));
            }
            let scheduled_time = event.schedule();
            if !scheduled_time.is_finite() || scheduled_time < 0.0 {
                return Err(ApiError::new(400, "invalid scheduled time"));
            }
            let semester =
                semester_for_timestamp(scheduled_time as i64).map_err(ApiError::invalid)?;
            if semester != semester_for_timestamp(now).map_err(ApiError::internal)? {
                return Err(ApiError::new(
                    409,
                    "scheduled refresh belongs to a different semester",
                ));
            }
            Catalogue::new(&env)?.refresh(&semester).await
        }
        .await;
        result
            .map(|()| JsValue::UNDEFINED)
            .map_err(|error| JsValue::from_str(&error.to_string()))
    }));
    context.wait_until(promise.clone())?;
    Ok(promise)
}
