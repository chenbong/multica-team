import type { LucideIcon } from "lucide-react";
import { ChevronRight } from "lucide-react";
import { useT } from "../../i18n";

export const createMethodCardClassName =
  "group relative flex min-h-56 flex-col items-start rounded-xl border bg-card p-5 text-left " +
  "transition-[border-color,background-color,transform] hover:-translate-y-0.5 hover:border-primary/40 hover:bg-accent/30 " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

export function CreateMethodCardContent({
  icon: Icon,
  title,
  description,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
}) {
  const { t } = useT("agents");
  return (
    <>
      <span className="flex size-11 items-center justify-center rounded-lg bg-muted text-muted-foreground group-hover:text-foreground">
        <Icon className="size-5" aria-hidden="true" />
      </span>
      <span className="mt-7 text-title-sm font-semibold">{title}</span>
      <span className="mt-2 text-body leading-6 text-muted-foreground">{description}</span>
      <span className="mt-auto flex items-center gap-1 pt-5 text-caption font-medium text-foreground">
        {t(($) => $.creation_studio.continue)}
        <ChevronRight className="size-3.5 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
      </span>
    </>
  );
}
