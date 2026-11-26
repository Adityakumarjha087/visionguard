"use client";
import { ShieldOff, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { useAuth } from "@/components/auth/auth-provider";
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from "@/components/common/states";
import { ConfirmDialog } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { NativeSelect } from "@/components/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAsync } from "@/hooks/use-async";
import { api, errorMessage } from "@/lib/api";
import type { Role, User } from "@/lib/types";
import { formatDateTime } from "@/lib/utils";

const ROLES: Role[] = ["admin", "operator", "viewer"];

export default function UsersPage() {
  const { can, user: me } = useAuth();
  const isAdmin = can("admin");
  const users = useAsync(() => api.listUsers(), [], isAdmin);

  if (!isAdmin) {
    return <EmptyState icon={ShieldOff} title="Admins only" description="You need the admin role to manage users." />;
  }

  async function changeRole(u: User, role: Role) {
    try {
      const updated = await api.updateUser(u.id, { role });
      users.setData((prev) => prev?.map((x) => (x.id === u.id ? updated : x)));
      toast.success(`${updated.email} is now ${updated.role}`);
    } catch (e) {
      toast.error("Role change failed", { description: errorMessage(e) });
    }
  }

  async function remove(u: User) {
    try {
      await api.deleteUser(u.id);
      users.setData((prev) => prev?.filter((x) => x.id !== u.id));
      toast.success(`Deleted ${u.email}`);
    } catch (e) {
      toast.error("Delete failed", { description: errorMessage(e) });
    }
  }

  return (
    <>
      <PageHeader title="Users" description="Manage accounts and roles" />
      <Card>
        {users.error ? (
          <ErrorState error={users.error} onRetry={users.reload} className="m-4" />
        ) : !users.data ? (
          <div className="p-4">
            <TableSkeleton rows={3} />
          </div>
        ) : !users.data.length ? (
          <EmptyState icon={Users} title="No users" className="m-4" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="sr-only">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.data.map((u) => {
                const self = u.id === me?.id;
                return (
                  <TableRow key={u.id} data-testid="user-row">
                    <TableCell className="font-medium">
                      {u.name} {self ? <span className="text-xs text-muted-foreground">(you)</span> : null}
                    </TableCell>
                    <TableCell>{u.email}</TableCell>
                    <TableCell>
                      <NativeSelect
                        className="w-36"
                        value={u.role}
                        disabled={self}
                        aria-label={`Role for ${u.email}`}
                        onChange={(e) => void changeRole(u, e.target.value as Role)}
                      >
                        {ROLES.map((r) => (
                          <option key={r} value={r}>
                            {r}
                          </option>
                        ))}
                      </NativeSelect>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{formatDateTime(u.created_at)}</TableCell>
                    <TableCell className="text-right">
                      {!self ? (
                        <ConfirmDialog
                          title={`Delete ${u.email}?`}
                          description="The user loses access immediately."
                          onConfirm={() => remove(u)}
                          trigger={
                            <Button variant="ghost" size="icon" aria-label={`Delete ${u.email}`}>
                              <Trash2 className="text-destructive" />
                            </Button>
                          }
                        />
                      ) : null}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Card>
    </>
  );
}
