import { forwardRef } from "react";
import type { InputHTMLAttributes } from "react";

import "./Input.css";

export type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "style" | "color" | "size">;

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ className, ...props }, ref) {
  return <input ref={ref} className={["cc-input", className].filter(Boolean).join(" ")} {...props} />;
});
