import { forwardRef, useId } from "react";
import type { CSSProperties, HTMLAttributes } from "react";

import "./Progress.css";

type ProgressLabel = { label: string; "aria-label"?: string } | { label?: never; "aria-label": string };
export type ProgressProps = Omit<HTMLAttributes<HTMLDivElement>, "style" | "color" | "aria-label"> &
  ProgressLabel & { value: number; min?: number; max?: number };

export const Progress = forwardRef<HTMLDivElement, ProgressProps>(function Progress(
  { className, label, value, min = 0, max = 100, "aria-label": ariaLabel, ...props },
  ref,
) {
  const labelID = useId();
  const safeMax = Math.max(min, max);
  const current = Math.min(safeMax, Math.max(min, value));
  const range = safeMax - min;
  const percentage = range === 0 ? 100 : ((current - min) / range) * 100;
  return <div ref={ref} className={["cc-progress", className].filter(Boolean).join(" ")} {...props}>
    {label ? <span id={labelID} className="cc-progress__label">{label}</span> : null}
    <div
      className="cc-progress__track"
      role="progressbar"
      aria-label={ariaLabel}
      aria-labelledby={label ? labelID : undefined}
      aria-valuemin={min}
      aria-valuemax={safeMax}
      aria-valuenow={current}
    ><span className="cc-progress__value" style={{ "--cc-progress-value": `${percentage}%` } as CSSProperties} /></div>
  </div>;
});
