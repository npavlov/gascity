import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import "./Text.css";

export type TextVariant = "body" | "caption" | "label" | "title" | "code";
export interface TextProps extends Omit<HTMLAttributes<HTMLSpanElement>, "style" | "color"> {
  variant?: TextVariant;
}

export const Text = forwardRef<HTMLSpanElement, TextProps>(function Text(
  { className, variant = "body", ...props },
  ref,
) {
  return <span ref={ref} className={["cc-text", className].filter(Boolean).join(" ")} data-variant={variant} {...props} />;
});
