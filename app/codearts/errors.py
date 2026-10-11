from __future__ import annotations


class CodeArtsUpstreamError(RuntimeError):
    def __init__(
        self,
        message: str,
        *,
        status_code: int | None = None,
        category: str = "upstream",
        retryable: bool = False,
        retry_after_seconds: float | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.category = category
        self.retryable = retryable
        self.retry_after_seconds = retry_after_seconds


def classify_status(status_code: int) -> tuple[str, bool]:
    if status_code == 429:
        return "rate_limit", True
    if status_code in {401, 403}:
        return "auth", True
    if status_code in {408, 425}:
        return "timeout", True
    if 500 <= status_code <= 599:
        return "server", True
    return f"http_{status_code}", False
