import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./Stack.css";

export type Space = "0" | "1" | "2" | "3" | "4" | "5" | "6" | "8" | "10" | "12";
export interface StackProps extends Omit<HTMLAttributes<HTMLDivElement>, "style" | "color"> {
  gap?: Space;
}

export const Stack = forwardRef<HTMLDivElement, StackProps>(function Stack(
  { className, gap = "3", ...props },
  ref,
) {
  return <div ref={ref} className={["cc-stack", className].filter(Boolean).join(" ")} data-gap={gap} {...props} />;
});
