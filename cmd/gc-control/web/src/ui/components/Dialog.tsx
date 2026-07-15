import type { ReactElement, ReactNode } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";

import "./Dialog.css";

export interface DialogProps {
  title: string;
  trigger: ReactElement;
  children: ReactNode;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function Dialog({ title, trigger, children, defaultOpen, open, onOpenChange }: DialogProps) {
  return <RadixDialog.Root defaultOpen={defaultOpen} open={open} onOpenChange={onOpenChange}>
    <RadixDialog.Trigger asChild>{trigger}</RadixDialog.Trigger>
    <RadixDialog.Portal><RadixDialog.Overlay className="cc-dialog__overlay" /><RadixDialog.Content className="cc-dialog" aria-describedby={undefined}><RadixDialog.Title className="cc-dialog__title">{title}</RadixDialog.Title><div className="cc-dialog__body">{children}</div></RadixDialog.Content></RadixDialog.Portal>
  </RadixDialog.Root>;
}
