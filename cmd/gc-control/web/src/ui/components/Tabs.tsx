import { forwardRef } from "react";
import type { HTMLAttributes, ReactNode } from "react";
import * as RadixTabs from "@radix-ui/react-tabs";

import "./Tabs.css";

export interface TabItem { id: string; label: string; content: ReactNode; disabled?: boolean }
export interface TabsProps extends Omit<HTMLAttributes<HTMLDivElement>, "style" | "color" | "defaultValue" | "onChange"> {
  items: TabItem[];
  defaultValue?: string;
  value?: string;
  onValueChange?: (value: string) => void;
}

export const Tabs = forwardRef<HTMLDivElement, TabsProps>(function Tabs(
  { className, items, defaultValue, value, onValueChange, "aria-label": ariaLabel, ...props },
  ref,
) {
  const initial = defaultValue ?? items[0]?.id;
  return <div ref={ref} className={["cc-tabs", className].filter(Boolean).join(" ")} {...props}>
    <RadixTabs.Root value={value} defaultValue={value === undefined ? initial : undefined} onValueChange={onValueChange}>
      <RadixTabs.List className="cc-tabs__list" aria-label={ariaLabel}>
        {items.map((item) => <RadixTabs.Trigger className="cc-tabs__trigger" key={item.id} value={item.id} disabled={item.disabled}>{item.label}</RadixTabs.Trigger>)}
      </RadixTabs.List>
      {items.map((item) => <RadixTabs.Content className="cc-tabs__content" key={item.id} value={item.id}>{item.content}</RadixTabs.Content>)}
    </RadixTabs.Root>
  </div>;
});
