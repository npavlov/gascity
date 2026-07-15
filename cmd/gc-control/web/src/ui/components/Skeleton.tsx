import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./Skeleton.css";

export type SkeletonProps = Omit<HTMLAttributes<HTMLDivElement>, "style" | "color" | "aria-hidden">;

export const Skeleton = forwardRef<HTMLDivElement, SkeletonProps>(function Skeleton({ className, ...props }, ref) {
  return <div ref={ref} className={["cc-skeleton", className].filter(Boolean).join(" ")} aria-hidden="true" {...props} />;
});
