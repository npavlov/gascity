import { forwardRef } from "react";
import type { TextareaHTMLAttributes } from "react";

import "./Textarea.css";

export type TextareaProps = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "style" | "color">;

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea({ className, ...props }, ref) {
  return <textarea ref={ref} className={["cc-textarea", className].filter(Boolean).join(" ")} {...props} />;
});
