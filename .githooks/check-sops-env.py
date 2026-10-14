#!/usr/bin/env python3
"""Validate staged SOPS dotenv structure and policy, not cryptographic integrity."""

import base64
import binascii
import os
import re
import subprocess
import sys


KEY = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
CIPHERTEXT = re.compile(
    r"ENC\[AES256_GCM,data:([A-Za-z0-9+/]+={0,2}),"
    r"iv:([A-Za-z0-9+/]+={0,2}),tag:([A-Za-z0-9+/]+={0,2}),type:str\]"
)
# Recipient configuration does not change which dotenv values must be encrypted.
RULE_KEYS = {
    "path_regex", "encrypted_regex", "age", "pgp", "kms", "gcp_kms",
    "azure_keyvault", "hc_vault", "key_groups", "shamir_threshold",
}
MODE_KEYS = {
    "sops_encrypted_regex", "sops_unencrypted_regex", "sops_encrypted_suffix",
    "sops_unencrypted_suffix", "sops_encrypted_comment_regex",
    "sops_unencrypted_comment_regex", "sops_mac_only_encrypted",
}
METADATA_KEYS = MODE_KEYS | {
    "sops_mac", "sops_version", "sops_lastmodified", "sops_shamir_threshold",
}
RECIPIENT_KEY = re.compile(
    r"sops_(?:key_groups__list_[0-9]+__map_)?(?:"
    r"age__list_[0-9]+__map_(?:recipient|enc)|"
    r"pgp__list_[0-9]+__map_(?:fp|created_at|enc)|"
    r"kms__list_[0-9]+__map_(?:arn|created_at|enc|aws_profile|context__map_[A-Za-z0-9_]+)|"
    r"gcp_kms__list_[0-9]+__map_(?:resource_id|created_at|enc)|"
    r"azure_kv__list_[0-9]+__map_(?:vault_url|name|version|created_at|enc)|"
    r"hc_vault__list_[0-9]+__map_(?:vault_address|engine_path|key_name|created_at|enc)"
    r")"
)


class ValidationError(Exception):
    """A fixed, payload-free diagnostic suitable for displaying to the user."""


def git(root, *args):
    try:
        result = subprocess.run(
            ["git", *args], cwd=root, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, check=False,
        )
    except OSError:
        raise ValidationError("cannot run Git") from None
    if result.returncode:
        raise ValidationError("cannot read staged Git data")
    return result.stdout


def paths(output):
    return [os.fsdecode(path) for path in output.split(b"\0") if path]


def encrypted(value):
    match = CIPHERTEXT.fullmatch(value)
    if match is None:
        return False
    try:
        for component in match.groups():
            base64.b64decode(component, validate=True)
    except (binascii.Error, ValueError):
        return False
    return True


def dotenv(content):
    try:
        text = content.decode("utf-8")
    except UnicodeError:
        raise ValidationError("dotenv data is not UTF-8") from None
    fields = {}
    for number, line in enumerate(text.splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        key = key.strip()
        if not separator or KEY.fullmatch(key) is None:
            raise ValidationError(f"malformed dotenv entry on line {number}")
        if key in fields:
            raise ValidationError(f"duplicate key {key!r} on line {number}")
        fields[key] = value.strip()
    return fields


def policy(rules, path):
    for number, rule in enumerate(rules, 1):
        if not isinstance(rule, dict):
            raise ValidationError(f".sops.yaml creation rule {number} is not a mapping")
        path_regex = rule.get("path_regex", "")
        if not isinstance(path_regex, str):
            raise ValidationError(f".sops.yaml creation rule {number} has invalid path_regex")
        try:
            matches = re.search(path_regex, path) is not None
        except (re.error, OverflowError, RecursionError):
            raise ValidationError(
                f".sops.yaml creation rule {number} has invalid path_regex"
            ) from None
        if not matches:
            continue
        if any(key not in RULE_KEYS for key in rule):
            raise ValidationError(
                f".sops.yaml creation rule {number} uses unsupported policy options"
            )
        if "encrypted_regex" not in rule:
            return None, None
        expression = rule["encrypted_regex"]
        if not isinstance(expression, str) or not expression:
            raise ValidationError(
                f".sops.yaml creation rule {number} has invalid encrypted_regex"
            )
        try:
            return expression, re.compile(expression)
        except (re.error, OverflowError, RecursionError):
            raise ValidationError(
                f".sops.yaml creation rule {number} has invalid encrypted_regex"
            ) from None
    raise ValidationError("no matching creation rule in staged .sops.yaml")


def validate(content, expression, regex):
    fields = dotenv(content)
    if not encrypted(fields.get("sops_mac", "")):
        raise ValidationError("sops_mac is missing or is not AES256_GCM ciphertext")

    present_modes = MODE_KEYS.intersection(fields)
    if regex is not None:
        if fields.get("sops_encrypted_regex") != expression:
            raise ValidationError("sops_encrypted_regex does not match staged .sops.yaml")
        if present_modes != {"sops_encrypted_regex"}:
            raise ValidationError("SOPS encryption mode metadata conflicts with encrypted_regex")
    else:
        if present_modes - {"sops_unencrypted_suffix"}:
            raise ValidationError("SOPS encryption mode metadata conflicts with encrypt-all policy")
        if (
            "sops_unencrypted_suffix" in fields
            and fields["sops_unencrypted_suffix"] != "_unencrypted"
        ):
            raise ValidationError("sops_unencrypted_suffix does not match the SOPS default")

    ordinary = [
        (key, value) for key, value in fields.items()
        if key not in METADATA_KEYS and RECIPIENT_KEY.fullmatch(key) is None
    ]
    if not ordinary:
        raise ValidationError("no ordinary dotenv data keys")
    for key, value in ordinary:
        # Even the default _unencrypted suffix cannot exempt ordinary data here.
        # SOPS retains empty dotenv values verbatim; they contain no plaintext secret.
        if value and (regex is None or regex.search(key)) and not encrypted(value):
            raise ValidationError(f"key {key!r} must contain AES256_GCM ciphertext")


def main():
    try:
        root = os.fsdecode(git(None, "rev-parse", "--show-toplevel").rstrip(b"\n"))
        selected = paths(git(
            root, "diff", "--cached", "--name-only", "--diff-filter=ACMR",
            "-z", "--", "*.sops.env",
        ))
        config_changed = git(
            root, "diff", "--cached", "--name-only", "-z", "--", ".sops.yaml",
        )
        if config_changed:
            selected = paths(git(root, "ls-files", "--cached", "-z", "--", "*.sops.env"))
        selected = sorted({path for path in selected if path.endswith(".sops.env")})
        if not selected:
            return 0

        try:
            import yaml
        except ImportError:
            raise ValidationError("PyYAML is required; install the Python3 YAML module") from None

        try:
            config_content = git(root, "show", ":.sops.yaml")
        except ValidationError:
            raise ValidationError("missing or unreadable staged root .sops.yaml") from None
        try:
            config = yaml.safe_load(config_content)
        except (yaml.YAMLError, ValueError, OverflowError, RecursionError):
            raise ValidationError("cannot parse staged .sops.yaml safely") from None
        if not isinstance(config, dict) or not isinstance(config.get("creation_rules"), list):
            raise ValidationError("staged .sops.yaml must contain a creation_rules list")
        rules = config["creation_rules"]
        rejected = False
        for path in selected:
            try:
                expression, regex = policy(rules, path)
                validate(git(root, "show", ":" + path), expression, regex)
            except ValidationError as error:
                print(f"check-sops-env: {path!r}: {error}", file=sys.stderr)
                rejected = True
        return 1 if rejected else 0
    except ValidationError as error:
        print(f"check-sops-env: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
