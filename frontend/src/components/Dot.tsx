export function Dot({ color, size = 7, pulse = false, hollow = false }: { color: string; size?: number; pulse?: boolean; hollow?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`dot${pulse ? " dot-pulse" : ""}`}
      style={{ width: size, height: size, background: hollow ? "transparent" : color, border: hollow ? `1.5px solid ${color}` : undefined }}
    />
  );
}
