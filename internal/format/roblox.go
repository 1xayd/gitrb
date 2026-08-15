// Package format bridges the Git snapshot format with Roblox's native place
// and model files. The rbxfile dependency is used for real Roblox binary
// places (.rbxl), binary models (.rbxm), and XML models (.rbxmx).
package format

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/robloxapi/rbxfile"
	"github.com/robloxapi/rbxfile/bin"
	rbxjson "github.com/robloxapi/rbxfile/json"
	rbxxml "github.com/robloxapi/rbxfile/xml"

	"gitrb/internal/protocol"
)

type ReadResult struct {
	Snapshot *protocol.Snapshot
	Warnings []string
}

type WriteResult struct {
	Warnings []string
}

type pendingNode struct {
	instance *rbxfile.Instance
	node     *protocol.Node
	path     string
}

func ReadModel(path string) (ReadResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ReadResult{}, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	var root *rbxfile.Root
	switch ext {
	case ".rbxl":
		root, err = bin.DeserializePlace(bytes.NewReader(data), nil)
	case ".rbxm", ".rbxmx":
		root, err = bin.DeserializeModel(bytes.NewReader(data), nil)
	default:
		return ReadResult{}, fmt.Errorf("input must end in .rbxl, .rbxm, or .rbxmx")
	}
	if err != nil {
		return ReadResult{}, fmt.Errorf("decode %s: %w", path, err)
	}
	projectName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	snapshot := protocol.NewSnapshot(projectName)
	paths := make(map[string]*rbxfile.Instance)
	instances := make([]pendingNode, 0)
	for i, inst := range root.Instances {
		if inst == nil {
			continue
		}
		n := makeNode(inst, i, "game", paths, &instances)
		snapshot.Roots = append(snapshot.Roots, n)
	}
	result := ReadResult{Snapshot: snapshot}
	for _, item := range instances {
		if item.node.Properties == nil {
			item.node.Properties = make(map[string]any)
		}
		for name, value := range item.instance.Properties {
			if name == "Name" || value == nil {
				continue
			}
			if name == "Source" && (item.node.ClassName == "Script" || item.node.ClassName == "LocalScript" || item.node.ClassName == "ModuleScript") {
				switch source := value.(type) {
				case rbxfile.ValueProtectedString:
					item.node.Script = &protocol.Script{Source: string(source)}
				case rbxfile.ValueString:
					item.node.Script = &protocol.Script{Source: string(source)}
				}
				continue
			}
			converted, warning := protocolValue(value, paths)
			if warning != "" {
				result.Warnings = append(result.Warnings, item.path+"."+name+": "+warning)
				continue
			}
			if converted != nil {
				item.node.Properties[name] = converted
			}
		}
	}
	if err := snapshot.Validate(); err != nil {
		return ReadResult{}, err
	}
	return result, nil
}

func WriteModel(path string, snapshot *protocol.Snapshot) (WriteResult, error) {
	if err := snapshot.Validate(); err != nil {
		return WriteResult{}, err
	}
	root, warnings, err := toRoot(snapshot)
	if err != nil {
		return WriteResult{}, err
	}
	var buf bytes.Buffer
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".rbxl":
		if err := bin.SerializePlace(&buf, nil, root); err != nil {
			return WriteResult{}, fmt.Errorf("encode binary place: %w", err)
		}
	case ".rbxm":
		if err := bin.SerializeModel(&buf, nil, root); err != nil {
			return WriteResult{}, fmt.Errorf("encode binary model: %w", err)
		}
	case ".rbxmx":
		if err := rbxxml.Serialize(&buf, nil, root); err != nil {
			return WriteResult{}, fmt.Errorf("encode XML model: %w", err)
		}
	default:
		return WriteResult{}, fmt.Errorf("output must end in .rbxl, .rbxm, or .rbxmx")
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Warnings: warnings}, nil
}

func makeNode(inst *rbxfile.Instance, order int, parentPath string, paths map[string]*rbxfile.Instance, records *[]pendingNode) *protocol.Node {
	name := inst.Name()
	if name == "" {
		name = inst.ClassName
	}
	path := parentPath + "." + name
	// A model can contain duplicate names. Keep references unambiguous by
	// adding the same occurrence suffix used by the project serializer.
	if _, exists := paths[path]; exists {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s~%d", path, i)
			if _, taken := paths[candidate]; !taken {
				path = candidate
				break
			}
		}
	}
	paths[path] = inst
	n := &protocol.Node{ID: inst.Reference, Name: name, ClassName: inst.ClassName, ParentPath: parentPath, Order: order, Children: make([]*protocol.Node, 0)}
	*records = append(*records, pendingNode{instance: inst, node: n, path: path})
	for i, child := range inst.Children {
		if child != nil {
			n.Children = append(n.Children, makeNode(child, i, path, paths, records))
		}
	}
	return n
}

func protocolValue(value rbxfile.Value, paths map[string]*rbxfile.Instance) (any, string) {
	refs := rbxfile.References{}
	switch v := value.(type) {
	case rbxfile.ValueString:
		return string(v), ""
	case rbxfile.ValueBinaryString:
		return map[string]any{"__type": "BinaryString", "base64": base64.StdEncoding.EncodeToString([]byte(v))}, ""
	case rbxfile.ValueProtectedString:
		return map[string]any{"__type": "ProtectedString", "value": string(v)}, ""
	case rbxfile.ValueContent:
		return string(v), ""
	case rbxfile.ValueBool:
		return bool(v), ""
	case rbxfile.ValueInt:
		return map[string]any{"__type": "Int", "value": int64(v)}, ""
	case rbxfile.ValueFloat:
		return map[string]any{"__type": "Float", "value": float64(v)}, ""
	case rbxfile.ValueDouble:
		return map[string]any{"__type": "Double", "value": float64(v)}, ""
	case rbxfile.ValueUDim:
		return map[string]any{"__type": "UDim", "scale": float64(v.Scale), "offset": int64(v.Offset)}, ""
	case rbxfile.ValueUDim2:
		x, _ := protocolValue(v.X, paths)
		y, _ := protocolValue(v.Y, paths)
		return map[string]any{"__type": "UDim2", "x": x, "y": y}, ""
	case rbxfile.ValueRay:
		o, _ := protocolValue(v.Origin, paths)
		d, _ := protocolValue(v.Direction, paths)
		return map[string]any{"__type": "Ray", "origin": o, "direction": d}, ""
	case rbxfile.ValueFaces:
		return map[string]any{"__type": "Faces", "right": v.Right, "top": v.Top, "back": v.Back, "left": v.Left, "bottom": v.Bottom, "front": v.Front}, ""
	case rbxfile.ValueAxes:
		return map[string]any{"__type": "Axes", "x": v.X, "y": v.Y, "z": v.Z}, ""
	case rbxfile.ValueBrickColor:
		return map[string]any{"__type": "BrickColor", "number": uint64(v)}, ""
	case rbxfile.ValueColor3:
		return map[string]any{"__type": "Color3", "r": float64(v.R), "g": float64(v.G), "b": float64(v.B)}, ""
	case rbxfile.ValueVector2:
		return map[string]any{"__type": "Vector2", "x": float64(v.X), "y": float64(v.Y)}, ""
	case rbxfile.ValueVector3:
		return map[string]any{"__type": "Vector3", "x": float64(v.X), "y": float64(v.Y), "z": float64(v.Z)}, ""
	case rbxfile.ValueCFrame:
		rotation := make([]float64, len(v.Rotation))
		for i, value := range v.Rotation {
			rotation[i] = float64(value)
		}
		position, _ := protocolValue(v.Position, paths)
		return map[string]any{"__type": "CFrame", "position": position, "rotation": rotation}, ""
	case rbxfile.ValueToken:
		return map[string]any{"__type": "Token", "value": uint64(v)}, ""
	case rbxfile.ValueReference:
		if v.Instance == nil {
			return map[string]any{"__type": "InstanceRef", "path": ""}, ""
		}
		for path, instance := range paths {
			if instance == v.Instance {
				return map[string]any{"__type": "InstanceRef", "path": path}, ""
			}
		}
		return map[string]any{"__type": "InstanceRef", "path": v.Instance.GetFullName()}, ""
	case rbxfile.ValueVector3int16:
		return map[string]any{"__type": "Vector3int16", "x": int64(v.X), "y": int64(v.Y), "z": int64(v.Z)}, ""
	case rbxfile.ValueVector2int16:
		return map[string]any{"__type": "Vector2int16", "x": int64(v.X), "y": int64(v.Y)}, ""
	case rbxfile.ValueNumberSequence:
		items := make([]any, len(v))
		for i, key := range v {
			items[i] = map[string]any{"time": float64(key.Time), "value": float64(key.Value), "envelope": float64(key.Envelope)}
		}
		return map[string]any{"__type": "NumberSequence", "keypoints": items}, ""
	case rbxfile.ValueColorSequence:
		items := make([]any, len(v))
		for i, key := range v {
			color, _ := protocolValue(key.Value, paths)
			items[i] = map[string]any{"time": float64(key.Time), "value": color, "envelope": float64(key.Envelope)}
		}
		return map[string]any{"__type": "ColorSequence", "keypoints": items}, ""
	case rbxfile.ValueNumberRange:
		return map[string]any{"__type": "NumberRange", "min": float64(v.Min), "max": float64(v.Max)}, ""
	case rbxfile.ValueRect2D:
		min, _ := protocolValue(v.Min, paths)
		max, _ := protocolValue(v.Max, paths)
		return map[string]any{"__type": "Rect", "min": min, "max": max}, ""
	case rbxfile.ValuePhysicalProperties:
		return map[string]any{"__type": "PhysicalProperties", "customPhysics": v.CustomPhysics, "density": float64(v.Density), "friction": float64(v.Friction), "elasticity": float64(v.Elasticity), "frictionWeight": float64(v.FrictionWeight), "elasticityWeight": float64(v.ElasticityWeight)}, ""
	case rbxfile.ValueColor3uint8:
		return map[string]any{"__type": "Color3uint8", "r": int64(v.R), "g": int64(v.G), "b": int64(v.B)}, ""
	case rbxfile.ValueInt64:
		return map[string]any{"__type": "Int64", "value": int64(v)}, ""
	case rbxfile.ValueSharedString:
		return map[string]any{"__type": "SharedString", "base64": base64.StdEncoding.EncodeToString([]byte(v))}, ""
	default:
		// Keep this call in the code path so a new rbxfile scalar remains easy
		// to inspect while retaining a clear warning in the exported snapshot.
		_ = rbxjson.ValueToJSONInterface(value, refs)
		return nil, "unsupported Roblox value type " + value.Type().String()
	}
}

func toRoot(snapshot *protocol.Snapshot) (*rbxfile.Root, []string, error) {
	root := rbxfile.NewRoot()
	paths := make(map[string]*rbxfile.Instance)
	instances := make([]pendingNode, 0)
	for i, node := range snapshot.Roots {
		if node == nil {
			continue
		}
		inst := makeInstance(node, nil)
		root.Instances = append(root.Instances, inst)
		collectInstances(inst, node, i, "game", paths, &instances)
	}
	warnings := make([]string, 0)
	for _, item := range instances {
		if item.node.Properties != nil {
			for name, raw := range item.node.Properties {
				value, warning, err := fromProtocolValue(name, raw, paths)
				if err != nil {
					warnings = append(warnings, item.path+"."+name+": "+err.Error())
					continue
				}
				if warning != "" {
					warnings = append(warnings, item.path+"."+name+": "+warning)
				}
				if value != nil {
					item.instance.Set(name, value)
				}
			}
		}
		if item.node.Script != nil {
			item.instance.Set("Source", rbxfile.ValueProtectedString(item.node.Script.Source))
		}
	}
	return root, warnings, nil
}

func makeInstance(node *protocol.Node, _ *rbxfile.Instance) *rbxfile.Instance {
	inst := rbxfile.NewInstance(node.ClassName, nil)
	inst.Set("Name", rbxfile.ValueString(node.Name))
	return inst
}

func collectInstances(inst *rbxfile.Instance, node *protocol.Node, order int, parentPath string, paths map[string]*rbxfile.Instance, records *[]pendingNode) {
	path := parentPath + "." + node.Name
	if _, exists := paths[path]; exists {
		path = fmt.Sprintf("%s~%d", path, order+1)
	}
	paths[path] = inst
	*records = append(*records, pendingNode{instance: inst, node: node, path: path})
	for i, child := range node.Children {
		if child == nil {
			continue
		}
		childInst := makeInstance(child, inst)
		inst.Children = append(inst.Children, childInst)
		collectInstances(childInst, child, i, path, paths, records)
	}
	inst.FixTree()
}

func fromProtocolValue(name string, raw any, paths map[string]*rbxfile.Instance) (rbxfile.Value, string, error) {
	if raw == nil {
		return nil, "", nil
	}
	if typed, ok := raw.(map[string]any); ok {
		if typeName, ok := typed["__type"].(string); ok {
			switch typeName {
			case "ProtectedString":
				return rbxfile.ValueProtectedString(asString(typed["value"])), "", nil
			case "BinaryString":
				data, err := base64.StdEncoding.DecodeString(asString(typed["base64"]))
				if err != nil {
					return nil, "", err
				}
				return rbxfile.ValueBinaryString(data), "", nil
			case "SharedString":
				data, err := base64.StdEncoding.DecodeString(asString(typed["base64"]))
				if err != nil {
					return nil, "", err
				}
				return rbxfile.ValueSharedString(data), "", nil
			case "InstanceRef":
				path := asString(typed["path"])
				if path == "" {
					return rbxfile.ValueReference{}, "", nil
				}
				instance, ok := paths[path]
				if !ok {
					return nil, "unresolved instance reference " + path, nil
				}
				return rbxfile.ValueReference{Instance: instance}, "", nil
			case "Int":
				return rbxfile.ValueInt(int32(asInt64(typed["value"]))), "", nil
			case "Float":
				return rbxfile.ValueFloat(float32(asFloat(typed["value"]))), "", nil
			case "Double":
				return rbxfile.ValueDouble(asFloat(typed["value"])), "", nil
			case "Int64":
				return rbxfile.ValueInt64(asInt64(typed["value"])), "", nil
			case "Token", "EnumItem":
				token := asInt64(firstNonNil(typed["value"], typed["number"]))
				return rbxfile.ValueToken(uint32(token)), "", nil
			case "BrickColor":
				brickColor := asInt64(firstNonNil(typed["number"], typed["value"]))
				return rbxfile.ValueBrickColor(uint32(brickColor)), "", nil
			case "Vector2":
				return rbxfile.ValueVector2{X: float32(asFloat(typed["x"])), Y: float32(asFloat(typed["y"]))}, "", nil
			case "Vector3":
				return rbxfile.ValueVector3{X: float32(asFloat(typed["x"])), Y: float32(asFloat(typed["y"])), Z: float32(asFloat(typed["z"]))}, "", nil
			case "Vector2int16":
				return rbxfile.ValueVector2int16{X: int16(asInt64(typed["x"])), Y: int16(asInt64(typed["y"]))}, "", nil
			case "Vector3int16":
				return rbxfile.ValueVector3int16{X: int16(asInt64(typed["x"])), Y: int16(asInt64(typed["y"])), Z: int16(asInt64(typed["z"]))}, "", nil
			case "Color3":
				return rbxfile.ValueColor3{R: float32(asFloat(typed["r"])), G: float32(asFloat(typed["g"])), B: float32(asFloat(typed["b"]))}, "", nil
			case "Color3uint8":
				return rbxfile.ValueColor3uint8{R: byte(asInt64(typed["r"])), G: byte(asInt64(typed["g"])), B: byte(asInt64(typed["b"]))}, "", nil
			case "UDim":
				return rbxfile.ValueUDim{Scale: float32(asFloat(typed["scale"])), Offset: int32(asInt64(typed["offset"]))}, "", nil
			case "UDim2":
				x, _, err := fromProtocolValue(name, typed["x"], paths)
				if err != nil {
					return nil, "", err
				}
				y, _, err := fromProtocolValue(name, typed["y"], paths)
				if err != nil {
					return nil, "", err
				}
				xu, okx := x.(rbxfile.ValueUDim)
				yu, oky := y.(rbxfile.ValueUDim)
				if !okx || !oky {
					return nil, "", fmt.Errorf("UDim2 requires UDim x and y")
				}
				return rbxfile.ValueUDim2{X: xu, Y: yu}, "", nil
			case "CFrame":
				position, _, err := fromProtocolValue(name, typed["position"], paths)
				if err != nil {
					return nil, "", err
				}
				p, ok := position.(rbxfile.ValueVector3)
				if !ok {
					return nil, "", fmt.Errorf("CFrame position must be Vector3")
				}
				rotation := [9]float32{}
				if values, ok := typed["rotation"].([]any); ok {
					for i := 0; i < len(rotation) && i < len(values); i++ {
						rotation[i] = float32(asFloat(values[i]))
					}
				}
				return rbxfile.ValueCFrame{Position: p, Rotation: rotation}, "", nil
			case "Ray":
				o, _, err := fromProtocolValue(name, typed["origin"], paths)
				if err != nil {
					return nil, "", err
				}
				d, _, err := fromProtocolValue(name, typed["direction"], paths)
				if err != nil {
					return nil, "", err
				}
				origin, oko := o.(rbxfile.ValueVector3)
				direction, okd := d.(rbxfile.ValueVector3)
				if !oko || !okd {
					return nil, "", fmt.Errorf("Ray requires Vector3 origin and direction")
				}
				return rbxfile.ValueRay{Origin: origin, Direction: direction}, "", nil
			case "Faces":
				return rbxfile.ValueFaces{Right: asBool(typed["right"]), Top: asBool(typed["top"]), Back: asBool(typed["back"]), Left: asBool(typed["left"]), Bottom: asBool(typed["bottom"]), Front: asBool(typed["front"])}, "", nil
			case "Axes":
				return rbxfile.ValueAxes{X: asBool(typed["x"]), Y: asBool(typed["y"]), Z: asBool(typed["z"])}, "", nil
			case "NumberRange":
				return rbxfile.ValueNumberRange{Min: float32(asFloat(typed["min"])), Max: float32(asFloat(typed["max"]))}, "", nil
			case "Rect":
				min, _, err := fromProtocolValue(name, typed["min"], paths)
				if err != nil {
					return nil, "", err
				}
				max, _, err := fromProtocolValue(name, typed["max"], paths)
				if err != nil {
					return nil, "", err
				}
				mi, okm := min.(rbxfile.ValueVector2)
				ma, oka := max.(rbxfile.ValueVector2)
				if !okm || !oka {
					return nil, "", fmt.Errorf("Rect requires Vector2 min and max")
				}
				return rbxfile.ValueRect2D{Min: mi, Max: ma}, "", nil
			case "PhysicalProperties":
				return rbxfile.ValuePhysicalProperties{CustomPhysics: asBool(typed["customPhysics"]), Density: float32(asFloat(typed["density"])), Friction: float32(asFloat(typed["friction"])), Elasticity: float32(asFloat(typed["elasticity"])), FrictionWeight: float32(asFloat(typed["frictionWeight"])), ElasticityWeight: float32(asFloat(typed["elasticityWeight"]))}, "", nil
			case "NumberSequence":
				keys, ok := typed["keypoints"].([]any)
				if !ok {
					return nil, "", fmt.Errorf("NumberSequence keypoints must be an array")
				}
				sequence := make(rbxfile.ValueNumberSequence, 0, len(keys))
				for _, item := range keys {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					sequence = append(sequence, rbxfile.ValueNumberSequenceKeypoint{Time: float32(asFloat(m["time"])), Value: float32(asFloat(m["value"])), Envelope: float32(asFloat(m["envelope"]))})
				}
				return sequence, "", nil
			case "ColorSequence":
				keys, ok := typed["keypoints"].([]any)
				if !ok {
					return nil, "", fmt.Errorf("ColorSequence keypoints must be an array")
				}
				sequence := make(rbxfile.ValueColorSequence, 0, len(keys))
				for _, item := range keys {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					colorValue, _, err := fromProtocolValue(name, m["value"], paths)
					if err != nil {
						return nil, "", err
					}
					color, ok := colorValue.(rbxfile.ValueColor3)
					if !ok {
						return nil, "", fmt.Errorf("ColorSequence keypoint value must be Color3")
					}
					sequence = append(sequence, rbxfile.ValueColorSequenceKeypoint{Time: float32(asFloat(m["time"])), Value: color, Envelope: float32(asFloat(m["envelope"]))})
				}
				return sequence, "", nil
			}
		}
		if inferred, ok := inferUntypedMap(name, typed); ok {
			return fromProtocolValue(name, inferred, paths)
		}
	}
	switch value := raw.(type) {
	case bool:
		return rbxfile.ValueBool(value), "", nil
	case string:
		return rbxfile.ValueString(value), "", nil
	case float64:
		if isIntegerProperty(name) {
			return rbxfile.ValueInt(int32(value)), "", nil
		}
		return rbxfile.ValueFloat(float32(value)), "", nil
	case float32:
		return rbxfile.ValueFloat(value), "", nil
	case int, int32, int64:
		return rbxfile.ValueInt(int32(asInt64(value))), "", nil
	default:
		return nil, "", fmt.Errorf("unsupported JSON value %T", raw)
	}
}

func inferUntypedMap(name string, value map[string]any) (any, bool) {
	if _, ok := value["x"]; ok {
		if _, hasZ := value["z"]; hasZ {
			return map[string]any{"__type": "Vector3", "x": value["x"], "y": value["y"], "z": value["z"]}, true
		}
		if _, hasY := value["y"]; hasY {
			if _, nested := value["x"].(map[string]any); nested {
				return map[string]any{"__type": "UDim2", "x": value["x"], "y": value["y"]}, true
			}
			return map[string]any{"__type": "Vector2", "x": value["x"], "y": value["y"]}, true
		}
	}
	if _, ok := value["r"]; ok {
		return map[string]any{"__type": "Color3", "r": value["r"], "g": value["g"], "b": value["b"]}, true
	}
	if _, ok := value["position"]; ok {
		return map[string]any{"__type": "CFrame", "position": value["position"], "rotation": value["rotation"]}, true
	}
	return nil, false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func asFloat(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case jsonNumber:
		return float64(value)
	default:
		return 0
	}
}

type jsonNumber float64

func asInt64(value any) int64 {
	return int64(math.Round(asFloat(value)))
}

func asBool(value any) bool {
	b, _ := value.(bool)
	return b
}

func isIntegerProperty(name string) bool {
	for _, candidate := range []string{"LayoutOrder", "ZIndex", "Order", "TransparencyMode", "BrickColor", "Material", "Shape", "Font", "TextSize", "BorderSizePixel", "MaxPlayers", "QualityLevel"} {
		if name == candidate {
			return true
		}
	}
	return false
}
