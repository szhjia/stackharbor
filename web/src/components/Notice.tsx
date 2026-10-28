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
    <Alert className="console-notice" variant={danger ? "destructive" : "default"}>
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  );
}
