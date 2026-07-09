// Mirrors internal/auth/password.go's ValidatePassword,
// internal/handlers/auth.go's validateUsername/validateEmail, and
// internal/handlers/rooms.go's validateRoomName exactly, so inline errors
// shown before submit match what the server would say. The server remains
// authoritative — this is UX only, not the security boundary.

const USERNAME_PATTERN = /^[a-zA-Z0-9_-]{3,32}$/;
const MAX_EMAIL_LEN = 255;
const MIN_PASSWORD_BYTES = 8;
const MAX_PASSWORD_BYTES = 72; // bcrypt's input limit
const MAX_ROOM_NAME_LEN = 255;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function validateUsername(username: string): string | null {
  if (!USERNAME_PATTERN.test(username)) {
    return "Username must be 3-32 characters and contain only letters, numbers, underscores, or hyphens";
  }
  return null;
}

export function validateEmail(email: string): string | null {
  if (email.length > MAX_EMAIL_LEN) {
    return "Email is too long";
  }
  // A light sanity check, not a full RFC 5322 parse — the server does the
  // authoritative net/mail.ParseAddress check.
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    return "Email is not a valid address";
  }
  return null;
}

// Byte length (not character length) to match bcrypt's 72-byte input limit
// exactly, same as Go's len() on a UTF-8 string. Upper/lower/digit checks
// are ASCII-only (Go's unicode.IsUpper/IsLower/IsDigit are technically
// Unicode-aware) — an accepted gap for a non-authoritative pre-check: the
// server remains the real validation boundary.
export function validatePassword(password: string): string | null {
  const byteLength = new TextEncoder().encode(password).length;
  if (byteLength < MIN_PASSWORD_BYTES || byteLength > MAX_PASSWORD_BYTES) {
    return `Password must be between ${MIN_PASSWORD_BYTES} and ${MAX_PASSWORD_BYTES} characters`;
  }

  const hasUpper = /[A-Z]/.test(password);
  const hasLower = /[a-z]/.test(password);
  const hasDigit = /[0-9]/.test(password);
  if (!hasUpper || !hasLower || !hasDigit) {
    return "Password must contain an uppercase letter, a lowercase letter, and a digit";
  }

  return null;
}

export function validateRoomName(name: string): string | null {
  if (name === "" || name.length > MAX_ROOM_NAME_LEN) {
    return `Name is required and must be 1-${MAX_ROOM_NAME_LEN} characters`;
  }
  return null;
}

export function isValidUuid(value: string): boolean {
  return UUID_PATTERN.test(value);
}
