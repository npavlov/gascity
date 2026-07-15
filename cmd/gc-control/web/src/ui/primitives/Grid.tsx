import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import type { Space } from "./Stack";
import "./Grid.css";

export interface GridProps extends Omit<HTMLAttributes<HTMLDivElement>, "style" | "color"> {
  gap?: Space;
}

export const Grid = forwardRef<HTMLDivElement, GridProps>(function Grid(
  { className, gap = "3", ...props },
  ref,
) {
  return <div ref={ref} className={["cc-grid", className].filter(Boolean).join(" ")} data-gap={gap} {...props} />;
});
