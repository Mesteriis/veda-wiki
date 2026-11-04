"""Command-line configuration and the service key file."""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import ipaddress
import os
import stat

from .limits import Limits

DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8790
MIN_KEY_LENGTH = 16


class ConfigError(Exception):
    """Invalid configuration; the message never contains the key."""


@dataclass(frozen=True)
class Settings:
    host: str
    port: int
    key: bytes
    device: str
    cache_dir: str | None
    limits: Limits
    log_level: str
    allowed_hosts: tuple[str, ...] = ()


def read_key(path: str) -> bytes:
    """Read the shared key; the file must be private (no group/other access)."""
    try:
        info = os.stat(path)
    except OSError:
        raise ConfigError("key file is not readable") from None
    if not stat.S_ISREG(info.st_mode):
        raise ConfigError("key file must be a regular file")
    if info.st_mode & 0o077:
        raise ConfigError("key file must not be accessible by group or others (chmod 600)")
    with open(path, "rb") as handle:
        key = handle.read().strip()
    if len(key) < MIN_KEY_LENGTH:
        raise ConfigError(f"key must be at least {MIN_KEY_LENGTH} bytes")
    return key


def is_loopback(host: str) -> bool:
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="knowledge-extractor",
                                     description="VEDA GLiNER2.5 extraction service")
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--allow-non-loopback", action="store_true",
                        help="permit a non-loopback bind (closed container networks only)")
    parser.add_argument("--allowed-host", action="append", default=[],
                        help="extra accepted Host header name (required with a non-loopback bind)")
    parser.add_argument("--key-file", required=True, help="file (0600) with the shared service key")
    parser.add_argument("--device", default="cpu", choices=("cpu", "mps", "cuda"))
    parser.add_argument("--cache-dir", default=None,
                        help="Hugging Face hub cache (default: HF_HUB_CACHE/HF_HOME)")
    parser.add_argument("--log-level", default="info", choices=("debug", "info", "warning", "error"))
    defaults = Limits()
    parser.add_argument("--max-body-bytes", type=int, default=defaults.max_body_bytes)
    parser.add_argument("--max-blocks", type=int, default=defaults.max_blocks)
    parser.add_argument("--max-block-chars", type=int, default=defaults.max_block_chars)
    parser.add_argument("--request-timeout", type=float, default=defaults.request_timeout)
    parser.add_argument("--inference-concurrency", type=int, default=defaults.inference_concurrency,
                        help="must be 1: concurrent GLiNER2 inference is not proven thread-safe")
    parser.add_argument("--window-tokens", type=int, default=defaults.window_tokens)
    parser.add_argument("--window-margin", type=int, default=defaults.window_margin)
    parser.add_argument("--overlap-tokens", type=int, default=defaults.overlap_tokens)
    return parser


def parse_settings(argv: list[str] | None = None) -> Settings:
    args = build_parser().parse_args(argv)
    if not is_loopback(args.host) and not args.allow_non_loopback:
        raise ConfigError("refusing a non-loopback bind without --allow-non-loopback")
    if not is_loopback(args.host) and not args.allowed_host:
        raise ConfigError("a non-loopback bind needs at least one --allowed-host")
    if not 0 < args.port < 65536:
        raise ConfigError("port must be in 1..65535")
    try:
        limits = Limits(max_body_bytes=args.max_body_bytes, max_blocks=args.max_blocks,
                        max_block_chars=args.max_block_chars, request_timeout=args.request_timeout,
                        inference_concurrency=args.inference_concurrency,
                        window_tokens=args.window_tokens, window_margin=args.window_margin,
                        overlap_tokens=args.overlap_tokens)
    except ValueError as exc:
        raise ConfigError(str(exc)) from None
    return Settings(host=args.host, port=args.port, key=read_key(args.key_file),
                    device=args.device, cache_dir=args.cache_dir, limits=limits,
                    log_level=args.log_level, allowed_hosts=tuple(args.allowed_host))
