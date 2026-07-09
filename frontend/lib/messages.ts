// Message-list-specific pure helpers (mirrors lib/rooms.ts's role for room
// display logic).

const timeFormatter = new Intl.DateTimeFormat(undefined, {
  hour: "numeric",
  minute: "2-digit",
  hour12: true,
});
const monthDayFormatter = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
const monthDayYearFormatter = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  year: "numeric",
});

function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

// Smart/relative timestamp (locked design decision): time-only for today
// ("2:45 PM"), "Yesterday 2:45 PM" for yesterday, "Jul 5, 2:45 PM" for an
// older message this year, "Jul 5, 2025, 2:45 PM" once the year rolls over.
export function formatMessageTimestamp(isoString: string, now: Date = new Date()): string {
  const date = new Date(isoString);
  const time = timeFormatter.format(date);

  if (isSameDay(date, now)) {
    return time;
  }

  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (isSameDay(date, yesterday)) {
    return `Yesterday ${time}`;
  }

  if (date.getFullYear() === now.getFullYear()) {
    return `${monthDayFormatter.format(date)}, ${time}`;
  }

  return `${monthDayYearFormatter.format(date)}, ${time}`;
}
