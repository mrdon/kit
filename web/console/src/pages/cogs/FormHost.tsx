import type { FormSpec } from './context';
import TankDetail from './TankDetail';
import BrewDay from './forms/BrewDay';
import Move from './forms/Move';
import PackageForm from './forms/Package';
import { Confirm, Level, PlanInfo, Spill, Variant } from './forms/Small';
import Repack from './forms/Repack';
import OutNotSold from './forms/OutNotSold';
import Count from './forms/Count';

// FormHost renders whatever sheet is open. Opening another form from a
// sheet replaces it; closing any of them goes back to the screen.
export default function FormHost({ spec }: { spec: FormSpec }) {
  switch (spec.kind) {
    case 'tank':
      return <TankDetail vesselId={spec.vesselId} />;
    case 'brew':
      return <BrewDay vesselId={spec.vesselId} planEntryId={spec.planEntryId} />;
    case 'move':
      return <Move vesselId={spec.vesselId} toVesselId={spec.toVesselId} />;
    case 'package':
      return <PackageForm vesselId={spec.vesselId} />;
    case 'spill':
      return <Spill vesselId={spec.vesselId} />;
    case 'level':
      return <Level vesselId={spec.vesselId} />;
    case 'confirm':
      return <Confirm action={spec.action} vesselId={spec.vesselId} />;
    case 'variant':
      return <Variant vesselId={spec.vesselId} />;
    case 'repack':
      return <Repack />;
    case 'out':
      return <OutNotSold />;
    case 'count':
      return <Count />;
    case 'plan':
      return <PlanInfo planEntryId={spec.planEntryId} />;
  }
}
