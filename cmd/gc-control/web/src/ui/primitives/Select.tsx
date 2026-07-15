import { forwardRef } from "react";
import type { SelectHTMLAttributes } from "react";

import "./Select.css";

export type SelectProps = Omit<SelectHTMLAttributes<HTMLSelectElement>, "style" | "color" | "size">;

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select({ className, ...props }, ref) {
  return <select ref={ref} className={["cc-select", className].filter(Boolean).join(" ")} {...props} />;
});
