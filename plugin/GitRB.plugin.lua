-- gitrb Studio bridge
--
-- This is intentionally a single-file plugin so it can be installed as a
-- Script inside GitRB.rbxm. It talks only to the local gitrb process.

local HttpService = game:GetService("HttpService")
local Selection = game:GetService("Selection")
local CollectionService = game:GetService("CollectionService")
local ChangeHistoryService = game:GetService("ChangeHistoryService")

local PROTOCOL = 1
local SCHEMA = 1
local DEFAULT_ENDPOINT = "http://127.0.0.1:1648"
local DIRECT_LIMIT = 380000
local CHUNK_SIZE = 180000

local ROOT_NAMES = {
	"Workspace",
	"ReplicatedFirst",
	"ReplicatedStorage",
	"ServerScriptService",
	"ServerStorage",
	"StarterGui",
	"StarterPack",
	"StarterPlayer",
	"Lighting",
	"Teams",
	"SoundService",
	"TextChatService",
	"Chat",
	"MaterialService",
}

local COMMON_PROPERTIES = {
	"Archivable",
	"Active",
	"Enabled",
	"Visible",
	"Name",
	"Parent",
	"Position",
	"Orientation",
	"CFrame",
	"PivotOffset",
	"Size",
	"Color",
	"BrickColor",
	"Material",
	"Transparency",
	"Reflectance",
	"CanCollide",
	"CanTouch",
	"CanQuery",
	"Anchored",
	"Massless",
	"CastShadow",
	"CollisionGroup",
	"Shape",
	"MeshId",
	"TextureID",
	"Texture",
	"OffsetStudsU",
	"OffsetStudsV",
	"StudsPerTileU",
	"StudsPerTileV",
	"SoundId",
	"Volume",
	"PlaybackSpeed",
	"Looped",
	"RollOffMaxDistance",
	"RollOffMinDistance",
	"PlayOnRemove",
	"AnimationId",
	"Animation",
	"RunContext",
	"MaxActivationDistance",
	"ActionText",
	"ObjectText",
	"RequiresHandle",
	"Grip",
	"ToolTip",
	"Text",
	"TextColor3",
	"TextSize",
	"Font",
	"FontFace",
	"TextScaled",
	"TextWrapped",
	"RichText",
	"LineHeight",
	"BackgroundColor3",
	"BackgroundTransparency",
	"BorderColor3",
	"BorderSizePixel",
	"Size",
	"AnchorPoint",
	"LayoutOrder",
	"ZIndex",
	"Image",
	"ImageColor3",
	"ImageTransparency",
	"ScaleType",
	"TileSize",
	"CanvasSize",
	"CanvasPosition",
	"ScrollBarThickness",
	"AutomaticCanvasSize",
	"ClipsDescendants",
	"ResetOnSpawn",
	"AlwaysOnTop",
	"Adornee",
	"Face",
	"StudsOffset",
	"MaxDistance",
	"Brightness",
	"Range",
	"Shadows",
	"ClockTime",
	"Ambient",
	"OutdoorAmbient",
	"FogColor",
	"FogStart",
	"FogEnd",
	"Technology",
	"EnvironmentDiffuseScale",
	"EnvironmentSpecularScale",
	"GlobalShadows",
	"ExposureCompensation",
	"GeographicLatitude",
	"PrimaryPart",
	"ModelStreamingMode",
	"StreamingEnabled",
	"RespawnLocation",
	"TeamColor",
	"AutoAssignable",
	"MaxPlayers",
	"BodyAngularVelocity",
	"BodyVelocity",
	"Attachment0",
	"Attachment1",
	"LimitsEnabled",
	"UpperAngle",
	"LowerAngle",
	"TargetAngle",
	"TargetPosition",
}

local function getSetting(name, fallback)
	local value = plugin:GetSetting(name)
	if value == nil or value == "" then
		return fallback
	end
	return value
end

local endpoint = getSetting("gitrb.endpoint", DEFAULT_ENDPOINT)
local projectName = getSetting("gitrb.project", "")
local token = getSetting("gitrb.token", "")
local baseRevision = getSetting("gitrb.baseRevision", "")
local statusLabel
local endpointBox
local projectBox
local tokenBox

local function setSetting(name, value)
	plugin:SetSetting(name, value)
end

local function setStatus(message, isError)
	if statusLabel then
		statusLabel.Text = message
		statusLabel.TextColor3 = isError and Color3.fromRGB(255, 130, 130) or Color3.fromRGB(190, 220, 190)
	end
end

local function trimSlash(value)
	return string.gsub(value, "/+$", "")
end

local function request(method, path, body)
	local headers = { ["Content-Type"] = "application/json" }
	if token ~= "" then
		headers["X-GitRB-Token"] = token
	end
	local ok, response = pcall(function()
		return HttpService:RequestAsync({
			Url = trimSlash(endpoint) .. path,
			Method = method,
			Headers = headers,
			Body = body and HttpService:JSONEncode(body) or "",
		})
	end)
	if not ok then
		error("bridge request failed: " .. tostring(response))
	end
	local decoded
	local decodeOk, decodeResult = pcall(function()
		return HttpService:JSONDecode(response.Body)
	end)
	if decodeOk then
		decoded = decodeResult
	else
		decoded = { ok = false, error = response.Body }
	end
	if not response.Success or response.StatusCode >= 400 or decoded.ok == false then
		local detail = decoded.error or ("HTTP " .. tostring(response.StatusCode))
		if decoded.serverRevision and decoded.serverRevision ~= "" then
			detail = detail .. " (server revision " .. decoded.serverRevision .. ")"
		end
		error(detail)
	end
	return decoded
end

local function instancePath(instance)
	local parts = {}
	local current = instance
	while current and current ~= game do
		table.insert(parts, 1, current.Name)
		current = current.Parent
	end
	if #parts == 0 then
		return "game"
	end
	return "game." .. table.concat(parts, ".")
end

local function numberList(values)
	local result = {}
	for i, value in ipairs(values) do
		result[i] = value
	end
	return result
end

local function serializeValue(value, paths)
	local kind = typeof(value)
	if kind == "nil" or kind == "string" or kind == "boolean" or kind == "number" then
		return value
	elseif kind == "Instance" then
		return { __type = "InstanceRef", path = paths[value] or instancePath(value) }
	elseif kind == "Vector2" then
		return { __type = "Vector2", x = value.X, y = value.Y }
	elseif kind == "Vector3" then
		return { __type = "Vector3", x = value.X, y = value.Y, z = value.Z }
	elseif kind == "Vector2int16" then
		return { __type = "Vector2int16", x = value.X, y = value.Y }
	elseif kind == "Vector3int16" then
		return { __type = "Vector3int16", x = value.X, y = value.Y, z = value.Z }
	elseif kind == "Color3" then
		return { __type = "Color3", r = value.R, g = value.G, b = value.B }
	elseif kind == "BrickColor" then
		return { __type = "BrickColor", number = value.Number }
	elseif kind == "CFrame" then
		local components = { value:GetComponents() }
		return {
			__type = "CFrame",
			position = { __type = "Vector3", x = components[1], y = components[2], z = components[3] },
			rotation = numberList({ table.unpack(components, 4, 12) }),
		}
	elseif kind == "UDim" then
		return { __type = "UDim", scale = value.Scale, offset = value.Offset }
	elseif kind == "UDim2" then
		return { __type = "UDim2", x = serializeValue(value.X, paths), y = serializeValue(value.Y, paths) }
	elseif kind == "NumberRange" then
		return { __type = "NumberRange", min = value.Min, max = value.Max }
	elseif kind == "NumberSequence" then
		local keypoints = {}
		for _, keypoint in ipairs(value.Keypoints) do
			table.insert(keypoints, { time = keypoint.Time, value = keypoint.Value, envelope = keypoint.Envelope })
		end
		return { __type = "NumberSequence", keypoints = keypoints }
	elseif kind == "ColorSequence" then
		local keypoints = {}
		for _, keypoint in ipairs(value.Keypoints) do
			table.insert(keypoints, { time = keypoint.Time, value = serializeValue(keypoint.Value, paths), envelope = keypoint.Envelope })
		end
		return { __type = "ColorSequence", keypoints = keypoints }
	elseif kind == "Rect" then
		return { __type = "Rect", min = serializeValue(value.Min, paths), max = serializeValue(value.Max, paths) }
	elseif kind == "Ray" then
		return { __type = "Ray", origin = serializeValue(value.Origin, paths), direction = serializeValue(value.Direction, paths) }
	elseif kind == "Faces" then
		return { __type = "Faces", right = value.Right, top = value.Top, back = value.Back, left = value.Left, bottom = value.Bottom, front = value.Front }
	elseif kind == "Axes" then
		return { __type = "Axes", x = value.X, y = value.Y, z = value.Z }
	elseif kind == "PhysicalProperties" then
		return { __type = "PhysicalProperties", customPhysics = true, density = value.Density, friction = value.Friction, elasticity = value.Elasticity, frictionWeight = value.FrictionWeight, elasticityWeight = value.ElasticityWeight }
	elseif kind == "EnumItem" then
		return { __type = "EnumItem", enum = value.EnumType.Name, name = value.Name, value = value.Value }
	end
	return nil
end

local function safeRead(instance, property, paths)
	if property == "Name" or property == "Parent" or property == "Source" then
		return nil
	end
	local ok, value = pcall(function()
		return instance[property]
	end)
	if not ok then
		return nil
	end
	return serializeValue(value, paths)
end

local function collectStructure(instance, order, parentPath, paths)
	local path = parentPath .. "." .. instance.Name
	local node = {
		id = path .. "#" .. tostring(order),
		name = instance.Name,
		className = instance.ClassName,
		parentPath = parentPath,
		order = order,
		children = {},
	}
	paths[instance] = path
	for index, child in ipairs(instance:GetChildren()) do
		table.insert(node.children, collectStructure(child, index - 1, path, paths))
	end
	return node
end

local function fillNode(node, instance, paths)
	local properties = {}
	local seen = {}
	for _, property in ipairs(COMMON_PROPERTIES) do
		if not seen[property] then
			seen[property] = true
			local value = safeRead(instance, property, paths)
			if value ~= nil then
				properties[property] = value
			end
		end
	end
	node.properties = properties
	local attributes = {}
	local attributeOk, rawAttributes = pcall(function()
		return instance:GetAttributes()
	end)
	if attributeOk then
		for name, value in pairs(rawAttributes) do
			attributes[name] = serializeValue(value, paths)
		end
	end
	node.attributes = attributes
	local tagsOk, tags = pcall(function()
		return CollectionService:GetTags(instance)
	end)
	if tagsOk then
		node.tags = tags
	end
	if instance:IsA("Script") or instance:IsA("LocalScript") or instance:IsA("ModuleScript") then
		local sourceOk, source = pcall(function()
			return instance.Source
		end)
		if sourceOk then
			node.script = { source = source }
		end
	end
	for index, child in ipairs(instance:GetChildren()) do
		fillNode(node.children[index], child, paths)
	end
end

local function selectedRoots()
	local selected = Selection:Get()
	local roots = {}
	local selectedSet = {}
	for _, instance in ipairs(selected) do
		selectedSet[instance] = true
	end
	for _, instance in ipairs(selected) do
		local parent = instance.Parent
		local hasSelectedAncestor = false
		while parent and parent ~= game do
			if selectedSet[parent] then
				hasSelectedAncestor = true
				break
			end
			parent = parent.Parent
		end
		if not hasSelectedAncestor then
			table.insert(roots, instance)
		end
	end
	table.sort(roots, function(a, b)
		return instancePath(a) < instancePath(b)
	end)
	return roots
end

local function gameRoots()
	local roots = {}
	for _, name in ipairs(ROOT_NAMES) do
		local ok, service = pcall(function()
			return game:GetService(name)
		end)
		if ok and service then
			table.insert(roots, service)
		end
	end
	return roots
end

local function makeSnapshot(roots)
	local paths = {}
	local snapshot = {
		schemaVersion = SCHEMA,
		project = projectName ~= "" and projectName or game.Name,
		placeId = game.PlaceId,
		gameId = game.GameId,
		roots = {},
	}
	for index, instance in ipairs(roots) do
		local parentPath = instance.Parent and instancePath(instance.Parent) or "game"
		local node = collectStructure(instance, index - 1, parentPath, paths)
		table.insert(snapshot.roots, node)
	end
	for index, instance in ipairs(roots) do
		fillNode(snapshot.roots[index], instance, paths)
	end
	return snapshot
end

local function utf8SafeEnd(value, startIndex, maxEnd)
	local finish = math.min(#value, maxEnd)
	while finish > startIndex and finish <= #value do
		local byte = string.byte(value, finish)
		if not byte or byte < 128 or byte >= 192 then
			break
		end
		finish = finish - 1
	end
	return finish
end

local function splitString(value, size)
	local chunks = {}
	local startIndex = 1
	while startIndex <= #value do
		local finish = utf8SafeEnd(value, startIndex, startIndex + size - 1)
		if finish < startIndex then
			finish = math.min(#value, startIndex + size - 1)
		end
		table.insert(chunks, string.sub(value, startIndex, finish))
		startIndex = finish + 1
	end
	return chunks
end

local function pushSnapshot(snapshot)
	local snapshotJSON = HttpService:JSONEncode(snapshot)
	if #snapshotJSON <= DIRECT_LIMIT then
		local result = request("POST", "/v1/sync/push", {
			protocol = PROTOCOL,
			project = snapshot.project,
			baseRevision = baseRevision,
			source = "roblox-studio",
			snapshot = snapshot,
		})
		baseRevision = result.revision or baseRevision
		projectName = result.project or snapshot.project
		setSetting("gitrb.baseRevision", baseRevision)
		setSetting("gitrb.project", projectName)
		return result
	end
	local chunks = splitString(snapshotJSON, CHUNK_SIZE)
	local start = request("POST", "/v1/sync/push/start", {
		protocol = PROTOCOL,
		project = snapshot.project,
		baseRevision = baseRevision,
		totalChunks = #chunks,
		size = #snapshotJSON,
	})
	local result
	for index, chunk in ipairs(chunks) do
		result = request("POST", "/v1/sync/push/chunk", {
			uploadId = start.uploadId,
			index = index - 1,
			total = #chunks,
			data = chunk,
		})
	end
	baseRevision = result.revision or baseRevision
	projectName = result.project or snapshot.project
	setSetting("gitrb.baseRevision", baseRevision)
	setSetting("gitrb.project", projectName)
	return result
end

local function pullSnapshot()
	local response = request("GET", "/v1/sync/pull")
	if response.mode == "inline" then
		return response.snapshot, response.revision
	end
	if response.mode ~= "chunked" then
		error("bridge returned an unknown pull mode")
	end
	local chunks = {}
	for index = 0, response.chunks - 1 do
		local chunk = request("GET", "/v1/sync/pull/chunk?transferId=" .. response.transferId .. "&index=" .. tostring(index))
		chunks[index + 1] = chunk.data
	end
	local snapshot, err = pcall(function()
		return HttpService:JSONDecode(table.concat(chunks))
	end)
	if not snapshot then
		error("could not decode chunked snapshot: " .. tostring(err))
	end
	return err, response.revision
end

local function resolvePath(path, desiredPaths)
	if not path or path == "" then
		return nil
	end
	if desiredPaths[path] then
		return desiredPaths[path]
	end
	local parts = string.split(path, ".")
	if parts[1] ~= "game" then
		return nil
	end
	local current = game
	for index = 2, #parts do
		current = current:FindFirstChild(parts[index])
		if not current then
			return nil
		end
	end
	return current
end

local function decodeValue(value, desiredPaths)
	if typeof(value) ~= "table" or not value.__type then
		return value
	end
	local kind = value.__type
	if kind == "Vector2" then
		return Vector2.new(value.x, value.y)
	elseif kind == "Vector3" then
		return Vector3.new(value.x, value.y, value.z)
	elseif kind == "Vector2int16" then
		return Vector2int16.new(value.x, value.y)
	elseif kind == "Vector3int16" then
		return Vector3int16.new(value.x, value.y, value.z)
	elseif kind == "Color3" then
		return Color3.new(value.r, value.g, value.b)
	elseif kind == "Color3uint8" then
		return Color3.fromRGB(value.r, value.g, value.b)
	elseif kind == "BrickColor" then
		return BrickColor.new(value.number or value.value)
	elseif kind == "CFrame" then
		local position = decodeValue(value.position, desiredPaths)
		local rotation = value.rotation or {}
		return CFrame.new(position.X, position.Y, position.Z, table.unpack(rotation))
	elseif kind == "UDim" then
		return UDim.new(value.scale, value.offset)
	elseif kind == "UDim2" then
		return UDim2.new(decodeValue(value.x, desiredPaths), decodeValue(value.y, desiredPaths))
	elseif kind == "NumberRange" then
		return NumberRange.new(value.min, value.max)
	elseif kind == "NumberSequence" then
		local keypoints = {}
		for _, keypoint in ipairs(value.keypoints or {}) do
			table.insert(keypoints, NumberSequenceKeypoint.new(keypoint.time, keypoint.value, keypoint.envelope or 0))
		end
		return NumberSequence.new(keypoints)
	elseif kind == "ColorSequence" then
		local keypoints = {}
		for _, keypoint in ipairs(value.keypoints or {}) do
			table.insert(keypoints, ColorSequenceKeypoint.new(keypoint.time, decodeValue(keypoint.value, desiredPaths), keypoint.envelope or 0))
		end
		return ColorSequence.new(keypoints)
	elseif kind == "Rect" then
		return Rect.new(decodeValue(value.min, desiredPaths), decodeValue(value.max, desiredPaths))
	elseif kind == "Ray" then
		return Ray.new(decodeValue(value.origin, desiredPaths), decodeValue(value.direction, desiredPaths))
	elseif kind == "Faces" then
		return Faces.new(value.right, value.top, value.back, value.left, value.bottom, value.front)
	elseif kind == "Axes" then
		return Axes.new(value.x, value.y, value.z)
	elseif kind == "PhysicalProperties" then
		if value.customPhysics == false then
			return PhysicalProperties.new(0.7, 0.3, 0.5, 1, 1)
		end
		return PhysicalProperties.new(value.density, value.friction, value.elasticity, value.frictionWeight, value.elasticityWeight)
	elseif kind == "EnumItem" then
		local enumType = Enum[value.enum]
		return enumType and enumType[value.name] or nil
	elseif kind == "InstanceRef" then
		return resolvePath(value.path, desiredPaths)
	elseif kind == "ProtectedString" then
		return value.value
	elseif kind == "Int" or kind == "Float" or kind == "Double" or kind == "Int64" or kind == "Token" then
		return value.value
	end
	return nil
end

local function findChild(parent, node, used)
	local candidates = {}
	for _, child in ipairs(parent:GetChildren()) do
		if child.Name == node.name and child.ClassName == node.className and not used[child] then
			table.insert(candidates, child)
		end
	end
	local instance = candidates[1]
	if not instance then
		local created, err = pcall(function()
			return Instance.new(node.className)
		end)
		if not created then
			error("cannot create " .. tostring(node.className) .. ": " .. tostring(err))
		end
		instance = err
	end
	used[instance] = true
	instance.Name = node.name
	if instance.Parent ~= parent then
		instance.Parent = parent
	end
	return instance
end

local function applySnapshot(snapshot, prune)
	if not snapshot or snapshot.schemaVersion ~= SCHEMA then
		error("unsupported snapshot schema")
	end
	local desiredPaths = {}
	local records = {}
	local roots = {}

	local function createNode(node, parent, parentPath, used)
		local instance = findChild(parent, node, used)
		local path = parentPath .. "." .. node.name
		desiredPaths[path] = instance
		table.insert(records, { node = node, instance = instance, path = path })
		local childUsed = {}
		for _, child in ipairs(node.children or {}) do
			createNode(child, instance, path, childUsed)
		end
		return instance
	end

	for _, node in ipairs(snapshot.roots or {}) do
		local parent = resolvePath(node.parentPath, desiredPaths) or game
		local rootUsed = {}
		local instance = createNode(node, parent, node.parentPath or "game", rootUsed)
		table.insert(roots, instance)
	end

	for _, record in ipairs(records) do
		local node = record.node
		local instance = record.instance
		for name, encoded in pairs(node.properties or {}) do
			if name ~= "Name" and name ~= "Parent" and name ~= "Source" then
				local value = decodeValue(encoded, desiredPaths)
				if value ~= nil then
					pcall(function()
						instance[name] = value
					end)
				end
			end
		end
		for name, encoded in pairs(node.attributes or {}) do
			local value = decodeValue(encoded, desiredPaths)
			if value ~= nil then
				pcall(function()
					instance:SetAttribute(name, value)
				end)
			end
		end
		if node.tags then
			for _, tag in ipairs(node.tags) do
				pcall(function()
					CollectionService:AddTag(instance, tag)
				end)
			end
		end
		if node.script and (instance:IsA("Script") or instance:IsA("LocalScript") or instance:IsA("ModuleScript")) then
			pcall(function()
				instance.Source = node.script.source or ""
			end)
		end
	end

	if prune then
		for _, record in ipairs(records) do
			local desired = {}
			for _, child in ipairs(record.node.children or {}) do
				local key = child.name .. "\0" .. child.className
				desired[key] = (desired[key] or 0) + 1
			end
			for _, child in ipairs(record.instance:GetChildren()) do
				local key = child.Name .. "\0" .. child.ClassName
				if desired[key] and desired[key] > 0 then
					desired[key] = desired[key] - 1
				else
					pcall(function()
						child:Destroy()
					end)
				end
			end
		end
	end
	Selection:Set(roots)
	ChangeHistoryService:SetWaypoint("gitrb pull")
end

local function runAction(label, callback)
	setStatus(label .. "...", false)
	task.spawn(function()
		local ok, result = pcall(callback)
		if ok then
			setStatus(result or (label .. " complete"), false)
		else
			setStatus(tostring(result), true)
		end
	end)
end

local function connectBridge()
	local response = request("GET", "/v1/health")
	projectName = response.project or projectName
	baseRevision = response.revision or baseRevision
	setSetting("gitrb.project", projectName)
	setSetting("gitrb.baseRevision", baseRevision)
	return "connected to " .. tostring(projectName) .. " at " .. tostring(baseRevision):sub(1, 12)
end

local function pushGame()
	local snapshot = makeSnapshot(gameRoots())
	local result = pushSnapshot(snapshot)
	return "pushed game: " .. tostring(#(result.changedFiles or {})) .. " files changed"
end

local function pushSelection()
	local roots = selectedRoots()
	if #roots == 0 then
		error("select at least one instance first")
	end
	local snapshot = makeSnapshot(roots)
	local result = pushSnapshot(snapshot)
	return "pushed selection: " .. tostring(#(result.changedFiles or {})) .. " files changed"
end

local function pull(prune)
	local snapshot, revision = pullSnapshot()
	applySnapshot(snapshot, prune)
	baseRevision = revision or baseRevision
	projectName = snapshot.project or projectName
	setSetting("gitrb.baseRevision", baseRevision)
	setSetting("gitrb.project", projectName)
	return prune and "pulled and pruned at " .. baseRevision:sub(1, 12) or "pulled at " .. baseRevision:sub(1, 12)
end

local toolbar = plugin:CreateToolbar("gitrb")
local openButton = toolbar:CreateButton("gitrb", "Open gitrb bridge", "")
openButton.ClickableWhenViewportHidden = true

local widgetInfo = DockWidgetPluginGuiInfo.new(Enum.InitialDockState.Float, true, false, 380, 470, 300, 300)
local widget = plugin:CreateDockWidgetPluginGui("GitRBDock", widgetInfo)
widget.Title = "gitrb"

local rootFrame = Instance.new("Frame")
rootFrame.BackgroundColor3 = Color3.fromRGB(35, 35, 38)
rootFrame.BorderSizePixel = 0
rootFrame.Size = UDim2.fromScale(1, 1)
rootFrame.Parent = widget

local padding = Instance.new("UIPadding")
padding.PaddingTop = UDim.new(0, 10)
padding.PaddingBottom = UDim.new(0, 10)
padding.PaddingLeft = UDim.new(0, 10)
padding.PaddingRight = UDim.new(0, 10)
padding.Parent = rootFrame

local layout = Instance.new("UIListLayout")
layout.Padding = UDim.new(0, 6)
layout.SortOrder = Enum.SortOrder.LayoutOrder
layout.Parent = rootFrame

local function addLabel(text, height)
	local label = Instance.new("TextLabel")
	label.BackgroundTransparency = 1
	label.TextColor3 = Color3.fromRGB(230, 230, 230)
	label.TextXAlignment = Enum.TextXAlignment.Left
	label.TextWrapped = true
	label.Font = Enum.Font.SourceSans
	label.TextSize = 14
	label.Text = text
	label.Size = UDim2.new(1, 0, 0, height or 24)
	label.Parent = rootFrame
	return label
end

local function addBox(value, placeholder)
	local box = Instance.new("TextBox")
	box.BackgroundColor3 = Color3.fromRGB(50, 50, 55)
	box.BorderSizePixel = 0
	box.TextColor3 = Color3.fromRGB(240, 240, 240)
	box.PlaceholderColor3 = Color3.fromRGB(150, 150, 150)
	box.Font = Enum.Font.Code
	box.TextSize = 13
	box.ClearTextOnFocus = false
	box.Text = value
	box.PlaceholderText = placeholder
	box.Size = UDim2.new(1, 0, 0, 28)
	box.Parent = rootFrame
	return box
end

local function addButton(text, callback)
	local button = Instance.new("TextButton")
	button.BackgroundColor3 = Color3.fromRGB(65, 92, 125)
	button.BorderSizePixel = 0
	button.TextColor3 = Color3.fromRGB(255, 255, 255)
	button.Font = Enum.Font.SourceSansSemibold
	button.TextSize = 14
	button.Text = text
	button.Size = UDim2.new(1, 0, 0, 30)
	button.Parent = rootFrame
	button.MouseButton1Click:Connect(function()
		endpoint = endpointBox.Text
		projectName = projectBox.Text
		token = tokenBox.Text
		setSetting("gitrb.endpoint", endpoint)
		setSetting("gitrb.project", projectName)
		setSetting("gitrb.token", token)
		runAction(text, callback)
	end)
	return button
end

addLabel("Local bridge on port 1648. Start `gitrb serve` before syncing.", 34)
addLabel("Endpoint")
endpointBox = addBox(endpoint, "http://127.0.0.1:1648")
addLabel("Project name (must match gitrb.json)")
projectBox = addBox(projectName, game.Name)
addLabel("Token (optional)")
tokenBox = addBox(token, "local token")
tokenBox.TextTransparency = 0.1

statusLabel = addLabel("Disconnected", 34)
addButton("Connect / refresh revision", connectBridge)
addButton("Push entire game", pushGame)
addButton("Push selection", pushSelection)
addButton("Pull from Git folder", function()
	return pull(false)
end)
addButton("Pull and prune managed trees", function()
	return pull(true)
end)

openButton.Click:Connect(function()
	widget.Enabled = not widget.Enabled
end)

widget.Enabled = getSetting("gitrb.open", true)
widget:GetPropertyChangedSignal("Enabled"):Connect(function()
	setSetting("gitrb.open", widget.Enabled)
end)
