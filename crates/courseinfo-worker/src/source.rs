use courseinfo_core::{DepartmentPage, parse_department};
use futures_util::StreamExt;
use worker::{AbortController, Date, Fetch, Request, RequestInit, RequestRedirect};

use crate::{
    error::{ApiError, Result},
    limits,
};

const MAX_HTML_BYTES: usize = 4 * 1024 * 1024;

pub(crate) struct AcquiredPage {
    pub observed_at_ms: i64,
    pub page: DepartmentPage,
}

struct AbortOnDrop(Option<AbortController>);

impl Drop for AbortOnDrop {
    fn drop(&mut self) {
        if let Some(controller) = self.0.take() {
            controller.abort();
        }
    }
}

pub(crate) fn now_ms() -> i64 {
    Date::now().as_millis() as i64
}

pub(crate) async fn acquire(
    base_url: &str,
    semester: &str,
    department: &str,
    deadline_ms: i64,
    timeout_ms: u64,
) -> Result<AcquiredPage> {
    let observed_at_ms = now_ms();
    if observed_at_ms >= deadline_ms {
        return Err(limits::expired());
    }
    let controller = AbortController::default();
    let signal = controller.signal();
    let mut cancellation = AbortOnDrop(Some(controller));
    let operation = async {
        let mut init = RequestInit::new();
        init.with_redirect(RequestRedirect::Manual);
        let url = format!("{base_url}/{semester}/subject/{department}");
        let request = Request::new_with_init(&url, &init).map_err(ApiError::internal)?;
        let mut response = Fetch::Request(request)
            .send_with_signal(&signal)
            .await
            .map_err(|_| ApiError::upstream("catalogue upstream request failed"))?;
        if response.status_code() != 200 {
            return Err(ApiError::upstream(format!(
                "catalogue upstream returned HTTP {}",
                response.status_code()
            )));
        }
        let mut stream = response
            .stream()
            .map_err(|_| ApiError::upstream("catalogue upstream body unavailable"))?;
        let mut bytes = Vec::new();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.map_err(|_| ApiError::upstream("catalogue upstream body failed"))?;
            if chunk.len() > MAX_HTML_BYTES.saturating_sub(bytes.len()) {
                return Err(ApiError::upstream(
                    "catalogue page exceeds the HTML size limit",
                ));
            }
            bytes.extend_from_slice(&chunk);
        }
        let html = String::from_utf8(bytes)
            .map_err(|_| ApiError::upstream("catalogue page is not UTF-8"))?;
        let page = parse_department(&html, semester, department)
            .map_err(|error| ApiError::upstream(format!("catalogue page rejected: {error}")))?;
        Ok(AcquiredPage {
            observed_at_ms,
            page,
        })
    };
    let result = limits::until(
        deadline_ms.min(observed_at_ms + timeout_ms as i64),
        operation,
    )
    .await;
    if result.is_ok() {
        cancellation.0.take();
    }
    result
}
