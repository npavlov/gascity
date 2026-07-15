import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./Spinner.css";

export interface SpinnerProps extends Omit<HTMLAttributes<HTMLSpanElement>, "style" | "color" | "children" | "aria-label"> { label: string }

export const Spinner = forwardRef<HTMLSpanElement, SpinnerProps>(function Spinner(
  { className, label, ...props },
  ref,
) {
  return <span ref={ref} className={["cc-spinner", className].filter(Boolean).join(" ")} role="status" aria-label={label} {...props}><span className="cc-spinner__indicator" aria-hidden="true" /></span>;
});
