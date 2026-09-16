import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import { createFlows } from "./flows";
import { palette, primitives } from "./primitives";
import type { SceneSettings, XRayModel } from "./model";

export interface CameraPose { position: [number, number, number]; target: [number, number, number]; zoom: number; }
export function createRenderer(host: HTMLDivElement, model: XRayModel, initial: SceneSettings, onSelect: (id: string) => void, onFailure: () => void, pose?: CameraPose) {
  const renderer = new THREE.WebGLRenderer({ antialias: true });
  const colors = palette(host), p = primitives(colors), scene = new THREE.Scene();
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5));
  renderer.setClearColor(colors.surface); renderer.toneMapping = THREE.ACESFilmicToneMapping;
  host.prepend(renderer.domElement);
  renderer.domElement.setAttribute("aria-hidden", "true");
  renderer.domElement.dataset.testid = "xray-canvas";
  const camera = new THREE.OrthographicCamera(-8, 8, 6, -6, .1, 150);
  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true; controls.enablePan = false; controls.minZoom = .5; controls.maxZoom = 3;
  controls.minPolarAngle = .2; controls.maxPolarAngle = Math.PI / 2.15;
  const width = Math.max(4.4, Math.ceil(Math.sqrt(model.nodes.length)) * 4.4);
  const center = new THREE.Vector3(-.6, 1, 0);
  const globePosition = new THREE.Vector3(-width / 2 - 1.5, 2.2, -1);
  scene.add(new THREE.HemisphereLight(colors.text, colors.border, 1.4));
  const key = new THREE.DirectionalLight(colors.primary, 2.3); key.position.set(3, 9, 3); scene.add(key);
  const rim = new THREE.DirectionalLight(colors.node, 1.6); rim.position.set(-4, 3, -5); scene.add(rim);
  const layers = { infra: new THREE.Group(), nodes: new THREE.Group(), pods: new THREE.Group() };
  Object.values(layers).forEach(g => scene.add(g));
  const grid = new THREE.GridHelper(40, 80, colors.border, colors.border);
  grid.position.y = -1.3; grid.material.transparent = true; grid.material.opacity = .15; scene.add(grid);
  const labelsHost = document.createElement("div"); labelsHost.className = "xray-labels"; labelsHost.setAttribute("aria-hidden", "true"); host.append(labelsHost);
  const labels: { el: HTMLDivElement; position: THREE.Vector3; layer?: keyof typeof layers; id?: string }[] = [];
  function label(text: string, position: THREE.Vector3, layer?: keyof typeof layers, id?: string) {
    const el = document.createElement("div"); el.className = "xray-label"; el.textContent = text;
    labelsHost.append(el); labels.push({ el, position, layer, id }); return el;
  }
  model.nodes.forEach(node => {
    const [x, , z] = node.position;
    if (node.name) p.chassis(layers.infra, x, z);
    p.translucent(layers.nodes, [x, 0, z], [3.8, .12, 4.05], colors.node, .28);
    p.translucent(layers.nodes, [x, .3, z], [3.8, .5, 4.05], colors.node, .08);
    p.wire(layers.nodes, [x, .25, z], [3.8, .62, 4.05], colors.node, .45);
    label(`${node.name || "Node not reported"} · ${node.shown}/${node.total} pods`, new THREE.Vector3(x, .12, z + 2.2), "nodes");
  });
  const pods = model.pods.map(item => {
    const group = new THREE.Group(); group.position.set(...item.position); layers.pods.add(group);
    const skin = p.translucent(group, [0, .42, 0], [.88, .8, .66], colors.primary); skin.userData.id = item.id;
    const border = p.wire(group, [0, .42, 0], [.88, .8, .66], colors.primary);
    const core = p.solid(group, [0, .22, 0], [.61, .22, .39], colors.border);
    core.material.emissive.set(colors.primary); core.material.emissiveIntensity = .2;
    for (let i = 0; i < 3; i++) p.solid(group, [-.21 + i * .21, .38, 0], [.1, .08, .2], colors.primary);
    p.solid(group, [0, -.03, 0], [1, .05, .77], colors.raised);
    p.wire(group, [0, -.02, 0], [1, .07, .77], colors.primary, .45);
    return { ...item, group, skin, border, core };
  });
  const selectedLabel = label("", new THREE.Vector3(), "pods", "selected"); selectedLabel.classList.add("xray-selected-label");
  const globe = new THREE.Group(); globe.position.copy(globePosition); scene.add(globe);
  for (let i = 0; i < 3; i++) {
    const ring = new THREE.Mesh(new THREE.TorusGeometry(.42, .009, 6, 64), new THREE.MeshBasicMaterial({ color: colors.primary }));
    if (i === 0) ring.rotation.x = Math.PI / 2; if (i === 1) ring.rotation.y = Math.PI / 2; globe.add(ring);
  }
  const globeLabel = label("Unresolved peers", globePosition.clone().add(new THREE.Vector3(0, .8, 0)));
  const flows = createFlows(scene, model, colors, globePosition);
  let settings = initial, active = true, frame = 0, timer = 0, disposed = false, failed = false, time = 0, last = 0;
  const point = new THREE.Vector3();
  function positionLabels() {
    labels.forEach(item => {
      if (!item.layer) return;
      item.el.hidden = !layers[item.layer].visible;
      point.copy(item.position);
      if (item.id) {
        const selected = pods.find(pod => pod.id === settings.selected);
        item.el.hidden = item.el.hidden || !selected;
        if (selected) { point.set(...selected.position); point.y = 1.15; selectedLabel.textContent = selected.pod.name; }
      }
      point.y += layers[item.layer].position.y; point.project(camera);
      item.el.style.left = `${(point.x * .5 + .5) * host.clientWidth}px`; item.el.style.top = `${(-point.y * .5 + .5) * host.clientHeight}px`;
    });
    point.copy(globePosition).add(new THREE.Vector3(0, .8, 0)).project(camera);
    globeLabel.style.left = `${(point.x * .5 + .5) * host.clientWidth}px`; globeLabel.style.top = `${(-point.y * .5 + .5) * host.clientHeight}px`;
  }
  function schedule() { clearTimeout(timer); timer = 0; if (!frame && !disposed && !failed && active && !document.hidden) frame = requestAnimationFrame(render); }
  function render(now: number) {
    frame = 0;
    if (disposed || failed || !active || document.hidden) return;
    const delta = Math.max(0, Math.min((now - last) / 1000, .05)); last = now;
    const moving = controls.update();
    if (!settings.paused) time += delta;
    flows.tick(time); positionLabels(); renderer.render(scene, camera);
    if (moving) schedule();
    else if (!settings.paused && settings.pods && model.flows.length) timer = window.setTimeout(schedule, 1000 / 30);
  }
  function update(next: SceneSettings) {
    settings = next;
    layers.infra.visible = next.infra; layers.nodes.visible = next.nodes; layers.pods.visible = next.pods;
    layers.nodes.position.y = .08 + next.spread / 100 * .8; layers.pods.position.y = .6 + next.spread / 100 * 2.1;
    p.opacity(next.opacity);
    const neighbours = new Set([next.selected, ...model.flows.filter(f => f.source === next.selected || f.target === next.selected).flatMap(f => [f.source, f.target])]);
    pods.forEach(pod => {
      pod.group.visible = !next.isolated || neighbours.has(pod.id);
      pod.border.material.opacity = pod.id === next.selected ? 1 : .55;
      pod.core.material.emissiveIntensity = pod.id === next.selected ? .5 : .2;
    });
    globe.visible = next.pods && model.flows.some(f => !f.target && (!next.isolated || f.source === next.selected)); globeLabel.hidden = !globe.visible;
    // Geometry is bounded; rebuild only for spacing, visibility/selection still
    // updates the line set. The loop never allocates per-frame geometry.
    flows.update(next, layers.pods.position.y);
    positionLabels(); schedule();
  }
  function reset() { camera.position.set(13, 12.5, 17); controls.target.copy(center); camera.zoom = 1; camera.updateProjectionMatrix(); controls.update(); schedule(); }
  function resize() {
    const w = host.clientWidth, h = host.clientHeight; if (!w || !h) return;
    const half = Math.max(5.3, width * .52, (width + 4) * .48 * h / w);
    camera.left = -half * w / h; camera.right = half * w / h; camera.top = half; camera.bottom = -half; camera.updateProjectionMatrix();
    renderer.setSize(w, h); schedule();
  }
  const ray = new THREE.Raycaster(), pointer = new THREE.Vector2(); let down: number[] | undefined;
  function pick(event: PointerEvent) {
    const r = renderer.domElement.getBoundingClientRect(); pointer.set((event.clientX - r.left) / r.width * 2 - 1, -(event.clientY - r.top) / r.height * 2 + 1);
    ray.setFromCamera(pointer, camera);
    return settings.pods ? ray.intersectObjects(pods.filter(p => p.group.visible).map(p => p.skin), false)[0]?.object.userData.id as string | undefined : undefined;
  }
  const pointerDown = (e: PointerEvent) => { down = [e.clientX, e.clientY]; };
  const pointerUp = (e: PointerEvent) => { if (down && Math.hypot(e.clientX - down[0], e.clientY - down[1]) < 5) { const id = pick(e); if (id) onSelect(id); } down = undefined; };
  const pointerMove = (e: PointerEvent) => { renderer.domElement.style.cursor = pick(e) ? "pointer" : "grab"; };
  const lost = (e: Event) => { e.preventDefault(); failed = true; cancelAnimationFrame(frame); clearTimeout(timer); frame = 0; onFailure(); };
  renderer.domElement.addEventListener("pointerdown", pointerDown); renderer.domElement.addEventListener("pointerup", pointerUp);
  renderer.domElement.addEventListener("pointermove", pointerMove); renderer.domElement.addEventListener("webglcontextlost", lost);
  controls.addEventListener("change", schedule); document.addEventListener("visibilitychange", schedule);
  const resizeObserver = new ResizeObserver(resize); resizeObserver.observe(host);
  const intersection = new IntersectionObserver(([entry]) => { active = entry.isIntersecting; if (!active) { cancelAnimationFrame(frame); clearTimeout(timer); frame = 0; } else schedule(); }); intersection.observe(host);
  reset(); if (pose) { camera.position.set(...pose.position); controls.target.set(...pose.target); camera.zoom = pose.zoom; camera.updateProjectionMatrix(); controls.update(); }
  resize(); update(initial);
  return {
    update, reset, zoom(factor: number) { camera.zoom = THREE.MathUtils.clamp(camera.zoom * factor, .5, 3); camera.updateProjectionMatrix(); schedule(); },
    dispose(): CameraPose {
      disposed = true; cancelAnimationFrame(frame); clearTimeout(timer); resizeObserver.disconnect(); intersection.disconnect();
      document.removeEventListener("visibilitychange", schedule); controls.removeEventListener("change", schedule); controls.dispose();
      renderer.domElement.removeEventListener("pointerdown", pointerDown); renderer.domElement.removeEventListener("pointerup", pointerUp); renderer.domElement.removeEventListener("pointermove", pointerMove); renderer.domElement.removeEventListener("webglcontextlost", lost);
      const geometry = new Set<THREE.BufferGeometry>(), materials = new Set<THREE.Material>();
      scene.traverse(object => { if (object instanceof THREE.Mesh || object instanceof THREE.LineSegments || object instanceof THREE.Sprite) { if ("geometry" in object) geometry.add(object.geometry); (Array.isArray(object.material) ? object.material : [object.material]).forEach(m => materials.add(m)); } });
      geometry.forEach(g => g.dispose()); materials.forEach(m => m.dispose()); flows.dispose(); p.dispose();
      renderer.dispose(); renderer.forceContextLoss(); renderer.domElement.remove(); labelsHost.remove();
      return { position: camera.position.toArray(), target: controls.target.toArray(), zoom: camera.zoom };
    },
  };
}
