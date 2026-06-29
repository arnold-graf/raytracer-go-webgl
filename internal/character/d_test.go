package character
import ("fmt"; "testing"; "raytracer/internal/vec")
func TestDiagAfterFix(t *testing.T) {
 r,_:=LoadRig(repoFile("data/rigs/humanoid.yaml"))
 w:=steppedGround{run:0.4,rise:0.25,base:2.0}
 loc:=NewLocomotor(r,vec.V{X:-1,Z:0},270,1.0,w)
 for i:=0;i<420;i++{loc.Update(1.0/60.0,r,w);if loc.HipPos.X<3.5{continue}
  pose:=ComputeLocomotionPose(r,&loc,"idle",w)
  f:=loc.Right;if f.Phase!=FootSwing||f.SwingTo.Y-f.PlantGroundY<0.1{continue}
  flex:=kneeFlexDeg(r,pose,"thigh_r","shin_r")
  fmt.Printf("f=%d t=%.2f foot=%.2f gy=%.2f flex=%.1f\n",i,f.SwingT,f.World.Y,loc.GroundY,flex)
 }
}
