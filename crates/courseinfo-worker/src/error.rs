use serde::Serialize;
use worker::Response;

pub(crate) type Result<T, E = ApiError> = std::result::Result<T, E>;

#[derive(Debug)]
pub(crate) struct ApiError {
    pub status: u16,
    pub message: String,
    allow: Option<&'static str>,
}

impl ApiError {
    pub fn new(status: u16, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
            allow: None,
        }
    }

    pub fn method_not_allowed(allow: &'static str) -> Self {
        let mut error = Self::new(405, "method not allowed");
        error.allow = Some(allow);
        error
    }

    pub fn invalid(error: impl std::fmt::Display) -> Self {
        Self::new(400, error.to_string())
    }

    pub fn upstream(message: impl Into<String>) -> Self {
        Self::new(502, message)
    }

    pub fn internal(error: impl std::fmt::Debug) -> Self {
        worker::console_error!("catalogue internal error: {:?}", error);
        Self::new(500, "catalogue storage or configuration failed")
    }

    pub fn response(&self) -> worker::Result<Response> {
        #[derive(Serialize)]
        struct ErrorBody<'a> {
            status: &'static str,
            message: &'a str,
        }
        let mut response = Response::from_json(&ErrorBody {
            status: "error",
            message: &self.message,
        })?
        .with_status(self.status);
        if let Some(allow) = self.allow {
            response.headers_mut().set("Allow", allow)?;
        }
        if self.status == 429 {
            response.headers_mut().set("Retry-After", "60")?;
        }
        Ok(response)
    }
}

impl std::fmt::Display for ApiError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.status, self.message)
    }
}
