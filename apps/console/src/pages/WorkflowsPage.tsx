import { useState } from "react"
import { AlertDialog, AlertDialogContent, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { reportDispatch } from "@/lib/api/report-dispatch"
import type { ControlPlaneSnapshot, DispatchIntent, SkillView, WorkflowView } from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, OperatorNotice, PageIntro, StatusBadge } from "./shared"

export function WorkflowsPage({ snapshot, dispatch, view = "definitions", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const [feedback, setFeedback] = useState("")
  return (
    <>
      <PageIntro eyebrow="EXECUTION / WORKFLOWS" title="Workflows" description="Definitions, immutable version facts, and promoted skills are separate from run admission and target execution." actions={<StatusBadge label={`${snapshot.workflows.length} definitions`} tone="info" />} />
      <OperatorNotice>Catalog rows come from the control plane. Starting a run still requires an approved definition, target snapshot, and per-device admission.</OperatorNotice>
      {feedback ? (
        <p role="status" className="mb-4 border border-border bg-muted/30 px-3 py-2 text-xs" aria-live="polite">
          {feedback}
        </p>
      ) : null}
      <Tabs value={view} onValueChange={onViewChange}>
        <TabsList className="rounded-none border border-border bg-background p-0" aria-label="Workflow Views">
          <TabsTrigger value="definitions" className="rounded-none">Definitions</TabsTrigger>
          <TabsTrigger value="versions" className="rounded-none">Versions</TabsTrigger>
          <TabsTrigger value="skills" className="rounded-none">Skills</TabsTrigger>
        </TabsList>
        <TabsContent value="definitions" className="mt-4"><WorkflowTable workflows={snapshot.workflows} /></TabsContent>
        <TabsContent value="versions" className="mt-4">
          <div className="space-y-2">
            <p className="text-xs text-muted-foreground">One immutable current version is retained per workflow definition.</p>
            {snapshot.workflows.map((workflow) => (
              <div key={workflow.id} className="flex flex-wrap justify-between gap-3 border border-border p-3 text-xs">
                <span className="font-medium">{workflow.name}</span>
                <span className="drift-data">Version {workflow.version}</span>
              </div>
            ))}
          </div>
        </TabsContent>
        <TabsContent value="skills" className="mt-4">
          <SkillsTable skills={snapshot.skills} dispatch={dispatch} onFeedback={setFeedback} />
        </TabsContent>
      </Tabs>
    </>
  )
}

function WorkflowTable({ workflows }: { workflows: readonly WorkflowView[] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = workflows.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      {workflows.length === 0 ? (
        <EmptyState label="No Workflow Definitions" detail="No workflow definitions are available in this workspace." />
      ) : (
        <ScrollArea className="h-[min(65vh,700px)] border border-border">
          <table className="w-full min-w-[760px] text-left text-xs">
            <caption className="sr-only">Workflow definitions</caption>
            <thead className="sticky top-0 bg-background">
              <tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground">
                <th className="p-3">Workflow</th>
                <th className="p-3">State</th>
                <th className="p-3">Targets</th>
                <th className="p-3">Safety</th>
                <th className="p-3">Steps</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((workflow) => (
                <tr key={workflow.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                  <td className="p-3">
                    <p className="font-medium">{workflow.name}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">{workflow.id}</p>
                  </td>
                  <td className="p-3"><StatusBadge label={workflow.state} tone={workflow.state === "published" ? "healthy" : "attention"} /></td>
                  <td className="p-3">{workflow.targetSelector}</td>
                  <td className="p-3 text-muted-foreground">{workflow.safetySummary}</td>
                  <td className="p-3">{workflow.stepCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollArea>
      )}
      <DataTablePagination page={page} pageSize={pageSize} total={workflows.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} />
    </>
  )
}

function SkillsTable({
  skills,
  dispatch,
  onFeedback,
}: {
  skills: readonly SkillView[]
  dispatch: DispatchIntent
  onFeedback: (message: string) => void
}) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = skills.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      {skills.length === 0 ? (
        <EmptyState label="No Skills" detail="No promoted skills are available in this workspace. Review and publish operate on an existing skill version." />
      ) : (
        <ScrollArea className="border border-border">
          <table className="w-full min-w-[920px] text-left text-xs">
            <caption className="sr-only">Promoted skills</caption>
            <thead>
              <tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground">
                <th className="p-3">Skill</th>
                <th className="p-3">Version</th>
                <th className="p-3">Trust</th>
                <th className="p-3">Capabilities</th>
                <th className="p-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((skill) => (
                <tr key={skill.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                  <td className="p-3">
                    <p className="font-medium">{skill.name}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">{skill.id}</p>
                  </td>
                  <td className="p-3">{skill.version}</td>
                  <td className="p-3"><StatusBadge label={skill.trust} tone={skill.trust === "approved" ? "healthy" : "attention"} /></td>
                  <td className="p-3 text-muted-foreground">{skill.capabilities.join(", ")}</td>
                  <td className="p-3">
                    <div className="flex flex-wrap gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!skill.versionId || skill.trust === "reviewed" || skill.trust === "approved"}
                        onClick={() => void reportDispatch(dispatch, { type: "reviewSkillVersion", versionId: skill.versionId ?? "", reason: "Operator review" }, onFeedback)}
                      >
                        Review Skill
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger
                          render={
                            <Button size="sm" variant="outline" disabled={!skill.versionId || skill.trust === "approved"}>
                              Publish Skill
                            </Button>
                          }
                        />
                        <AlertDialogContent
                          title="Publish Skill?"
                          description="Publishing makes this skill version eligible for workflow admission. Packages still cannot use shell, credentials, or unrestricted filesystem access."
                          confirmLabel="Confirm Publish"
                          onConfirm={() => void reportDispatch(dispatch, { type: "publishSkillVersion", versionId: skill.versionId ?? "", reason: "Operator publish", confirmed: true }, onFeedback)}
                        />
                      </AlertDialog>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollArea>
      )}
      <DataTablePagination page={page} pageSize={pageSize} total={skills.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} />
    </>
  )
}
