import { useNavigate, useParams } from 'react-router-dom';

// useDetailRoute binds a list page's open detail to the URL so detail views are
// addressable, shareable, and emailable. Given a list route `base` (e.g.
// '/events'), the open detail lives at `/{base}/{id}`: `openId` reflects the
// `:id` route param, `open(id)` navigates to it, and `close()` returns to the
// list. Pages mount at both `base` and `base/:id` (see main.tsx) and render the
// same component — only `openId` differs.
export function useDetailRoute(base: string) {
  const { id } = useParams();
  const navigate = useNavigate();
  return {
    openId: id ?? null,
    open: (rid: string) => navigate(`${base}/${rid}`),
    close: () => navigate(base),
  };
}
