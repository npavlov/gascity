import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./Badge.css";

export type StatusTone = "neutral" | "info" | "success" | "warning" | "danger";
export interface BadgeProps extends Omit<HTMLAttributes<HTMLSpanElement>, "style" | "color"> {
  tone?: StatusTone;
}

export const Badge = forwardRef<HTMLSpanElement, BadgeProps>(function Badge(
  { className, tone = "neutral", ...props },
  ref,
) {
  return <span ref={ref} className={["cc-badge", className].filter(Boolean).join(" ")} data-tone={tone} {...props} />;
});
