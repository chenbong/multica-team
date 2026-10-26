"""Wire authenticated Web activity without changing installed daemon clients."""
def extend(changes):
    def add(path, old, new):
        changes.setdefault(path, []).append((old, new))
    add("packages/core/api/client.ts", "  // Agents\n", '''  async recordLiveActivity(event:{event_id:string;page_view:boolean}):Promise<void> {
    await this.fetchRaw("/api/live/activity", {method:"POST",headers:{"Content-Type":"application/json"},keepalive:true,signal:AbortSignal.timeout(5000),body:JSON.stringify(event)});
  }

  // Agents
''')
    layout="apps/web/app/[workspaceSlug]/(dashboard)/layout.tsx"
    add(layout, 'import { Suspense } from "react";', 'import { Suspense } from "react";\nimport {LiveActivityReporter} from "@/components/live-activity-reporter";')
    add(layout, "            <WebNotificationBridge />", "            <WebNotificationBridge />\n            <Suspense fallback={null}><LiveActivityReporter/></Suspense>")
    login="apps/web/app/(auth)/login/page.tsx"
    add(login, 'import { Suspense', 'import {LiveDashboardLink} from "@/components/live-activity-reporter";\nimport { Suspense')
    add(login, '      <LoginPageContent />', '      <LoginPageContent />\n      <div className="fixed bottom-5 inset-x-0 text-center"><LiveDashboardLink/></div>')
