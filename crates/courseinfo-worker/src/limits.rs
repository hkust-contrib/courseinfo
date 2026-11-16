use std::{future::Future, pin::pin, time::Duration};

use futures_util::future::{Either, select};
use worker::{Delay, Env};

use crate::{
    error::{ApiError, Result},
    source::now_ms,
};

pub(crate) struct Limits {
    pub fetch_ms: u64,
    pub refresh_ms: u64,
}

impl Limits {
    pub fn from_env(env: &Env) -> Result<Self> {
        Ok(Self {
            fetch_ms: milliseconds(env, "FETCH_TIMEOUT_MS", 30_000)?,
            refresh_ms: milliseconds(env, "REFRESH_TIMEOUT_MS", 720_000)?,
        })
    }
}

fn milliseconds(env: &Env, name: &str, maximum: u64) -> Result<u64> {
    let value = env
        .var(name)
        .map_err(ApiError::internal)?
        .to_string()
        .parse::<u64>()
        .map_err(ApiError::internal)?;
    if value == 0 || value > maximum {
        return Err(ApiError::internal(format!(
            "{name} must be between 1 and {maximum}"
        )));
    }
    Ok(value)
}

// Dropping the losing future also drops its request cancellation guard. D1 has
// no cancellation API: an already-issued atomic statement may still commit.
pub(crate) async fn until<T>(
    deadline_ms: i64,
    operation: impl Future<Output = Result<T>>,
) -> Result<T> {
    let remaining = deadline_ms.saturating_sub(now_ms());
    if remaining <= 0 {
        return Err(expired());
    }
    match select(
        pin!(operation),
        pin!(Delay::from(Duration::from_millis(remaining as u64))),
    )
    .await
    {
        Either::Left((result, _)) => {
            if now_ms() >= deadline_ms {
                return Err(expired());
            }
            result
        }
        Either::Right(((), _)) => Err(expired()),
    }
}

pub(crate) fn expired() -> ApiError {
    ApiError::upstream("catalogue operation deadline exceeded")
}
