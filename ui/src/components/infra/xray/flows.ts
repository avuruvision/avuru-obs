import * as THREE from "three";
import type { SceneSettings, XRayModel } from "./model";
import type { Palette } from "./primitives";

export function createFlows(scene: THREE.Scene, model: XRayModel, colors: Palette, globe: THREE.Vector3) {
  const canvas = document.createElement("canvas"); canvas.width = canvas.height = 64;
  const ctx = canvas.getContext("2d")!;
  const gradient = ctx.createRadialGradient(32, 32, 0, 32, 32, 32);
  gradient.addColorStop(0, colors.text); gradient.addColorStop(.2, colors.primary); gradient.addColorStop(1, "transparent");
  ctx.fillStyle = gradient; ctx.fillRect(0, 0, 64, 64);
  const texture = new THREE.CanvasTexture(canvas);
  const entries = model.flows.map(flow => {
    const color = flow.edge.errors > 0 ? colors.error : flow.target ? colors.node : colors.primary;
    const line = new THREE.Mesh(new THREE.BufferGeometry(), new THREE.MeshBasicMaterial({ color, transparent: true, opacity: .65 }));
    const particles = Array.from({ length: 3 }, () => {
      const sprite = new THREE.Sprite(new THREE.SpriteMaterial({ map: texture, color, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }));
      sprite.scale.setScalar(.2); scene.add(sprite); return sprite;
    });
    scene.add(line);
    return { flow, line, particles, curve: new THREE.CatmullRomCurve3() };
  });
  let previousHeight: number | undefined;
  function update(settings: SceneSettings, height: number) {
    for (const entry of entries) {
      const { source, target } = entry.flow;
      if (height !== previousHeight) {
      const a = model.pods.find(p => p.id === source)!;
      const b = model.pods.find(p => p.id === target);
      const from = new THREE.Vector3(...a.position).add(new THREE.Vector3(0, height + .4, 0));
      const to = b ? new THREE.Vector3(...b.position).add(new THREE.Vector3(0, height + .4, 0)) : globe.clone();
      const middle = from.clone().lerp(to, .5); middle.y += 1;
      entry.curve = new THREE.CatmullRomCurve3([from, middle, to]);
      entry.line.geometry.dispose(); entry.line.geometry = new THREE.TubeGeometry(entry.curve, 28, .014, 4, false);
      }
      entry.line.visible = settings.pods && (!settings.isolated || source === settings.selected || target === settings.selected);
      entry.particles.forEach(p => { p.visible = entry.line.visible; });
    }
    previousHeight = height;
  }
  function tick(time: number) {
    entries.forEach(({ particles, curve }) => particles.forEach((p, i) => {
      if (p.visible) p.position.copy(curve.getPointAt((time * .14 + i / particles.length) % 1));
    }));
  }
  return { update, tick, dispose: () => texture.dispose() };
}
