import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./ActionBar.css";

export interface ActionBarProps extends Omit<HTMLAttributes<HTMLDivElement>, "style" | "color"> {
  "aria-label": string;
}

export const ActionBar = forwardRef<HTMLDivElement, ActionBarProps>(function ActionBar(
  { className, ...props },
  ref,
) {
  return <div ref={ref} role="toolbar" className={["cc-action-bar", className].filter(Boolean).join(" ")} data-wrap="true" {...props} />;
});
