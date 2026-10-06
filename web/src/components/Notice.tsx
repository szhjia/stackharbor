import { Alert, AlertTitle, AlertDescription } from "./ui/alert";
export function Notice({
  title,
  children,
  danger = false,
}: {
  title: string;
  children: React.ReactNode;
  danger?: boolean;
}) {
  return (
    <Alert variant={danger ? "destructive" : "default"}>
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  );
}
